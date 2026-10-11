package claude

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

const (
	// StructureBackendCLI runs the structuring tier through `claude -p`, on the
	// Claude Code login and its plan's usage limits. It is the default.
	StructureBackendCLI = "cli"
	// StructureBackendAPI runs the structuring tier through the Messages API
	// with the key in PLANWERK_ANTHROPIC_API_KEY, so its tokens draw from the
	// Console organization that key belongs to — the monthly API credits a Max
	// or Team plan includes — rather than from the plan's Claude Code limits.
	// A call the API cannot serve runs through `claude -p` (decision 122).
	StructureBackendAPI = "api"
	// DefaultStructureBackend is the compiled-in structuring backend; override
	// it with WithStructureAPI (--structure-backend /
	// PLANWERK_STRUCTURE_BACKEND) (decision 122).
	DefaultStructureBackend = StructureBackendCLI

	// apiPassSuffix marks the per-pass usage of a structuring call the API
	// served, so the run's breakdown tells the API-credit tokens from the
	// plan's. The API reports no cost, so these passes carry none.
	apiPassSuffix = " (api)"
	// apiMaxTokens caps max_tokens for a structuring call. The call streams,
	// so the cap guards against a runaway transcription, not a timeout; a
	// model whose own limit is lower gets that limit.
	apiMaxTokens = 64000
)

// ValidStructureBackend reports whether b is a structuring backend the root
// command accepts.
func ValidStructureBackend(b string) bool {
	return b == StructureBackendCLI || b == StructureBackendAPI
}

// structureAPIFunc runs one structuring prompt on the Messages API and returns
// the response text, the model id that served it, and its token counts. The
// SDK-backed apiStructurer provides it; tests substitute their own.
type structureAPIFunc func(ctx context.Context, model, effort, prompt string) (text, resolvedModel string, usage tokenUsage, err error)

// WithStructureAPI routes the structuring tier through the Messages API with
// apiKey (decision 122). An empty apiKey leaves the tier on `claude -p`; the
// root command rejects --structure-backend api without one before any session
// runs.
func WithStructureAPI(apiKey string) Option {
	return func(c *Client) {
		if apiKey != "" {
			c.structureAPI = newAPIStructurer(apiKey).run
		}
	}
}

// runStructureAPI runs a structuring spec on the Messages API when the Client
// has one and it has not been switched off for the run. ok is false whenever
// the caller must run the spec through `claude -p` instead: no API configured,
// an API error, a refusal, or a truncated response. Every such fallback is
// logged, and an error that will recur on every call (an exhausted credit
// balance, a rejected key) switches the API off for the rest of the run.
func (c *Client) runStructureAPI(spec runSpec, prompt string) (text, model string, ok bool) {
	if c.structureAPI == nil || c.structureAPIOff.Load() {
		return "", "", false
	}
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()
	stopProgress := startProgress(spec.label)
	defer stopProgress()

	text, model, usage, err := c.structureAPI(ctx, spec.model, spec.effort, prompt)
	if usage != (tokenUsage{}) {
		c.addUsage(spec.label+apiPassSuffix, usage, 0)
	}
	if err != nil {
		if apiErrorPersists(err) {
			if c.structureAPIOff.CompareAndSwap(false, true) {
				slog.Warn("structuring API unavailable; the structuring calls of this run use Claude Code", "pass", spec.label, "error", err)
			}
			return "", "", false
		}
		slog.Warn("structuring API call failed; running it through Claude Code", "pass", spec.label, "error", err)
		return "", "", false
	}
	return text, model, true
}

// apiErrorPersists reports whether err will fail every later call of the run
// alike: a rejected or unauthorized key (401, 403), a billing error (402), or
// the 400 the API returns once the organization's credit balance is spent.
func apiErrorPersists(err error) bool {
	var apiErr *anthropic.Error
	if !errors.As(err, &apiErr) {
		return false
	}
	switch apiErr.StatusCode {
	case http.StatusUnauthorized, http.StatusPaymentRequired, http.StatusForbidden:
		return true
	case http.StatusBadRequest:
		return strings.Contains(strings.ToLower(apiErr.Error()), "credit balance")
	}
	return false
}

// apiStructurer runs structuring prompts on the Messages API. It resolves each
// model name once per run: a Claude Code alias (haiku, sonnet, opus, fable)
// to the newest active model of that line, a full id to itself, both with
// the model's output limit and effort levels as the Models API lists them.
type apiStructurer struct {
	client anthropic.Client

	mu     sync.Mutex
	models map[string]anthropic.ModelInfo
}

// newAPIStructurer builds an apiStructurer on apiKey alone. The SDK's
// environment defaults stay off, so neither ANTHROPIC_API_KEY,
// ANTHROPIC_AUTH_TOKEN, ANTHROPIC_BASE_URL, nor an `ant` profile changes which
// organization the structuring calls bill. opts follow the key; tests point
// the client at a local server with them.
func newAPIStructurer(apiKey string, opts ...option.RequestOption) *apiStructurer {
	opts = append([]option.RequestOption{option.WithoutEnvironmentDefaults(), option.WithAPIKey(apiKey)}, opts...)
	return &apiStructurer{
		client: anthropic.NewClient(opts...),
		models: make(map[string]anthropic.ModelInfo),
	}
}

// run streams prompt to the resolved model and returns the text of the final
// message. A refusal or a response cut at max_tokens is an error: neither
// holds the complete transcription, and the caller runs the prompt through
// `claude -p` instead.
func (a *apiStructurer) run(ctx context.Context, model, effort, prompt string) (string, string, tokenUsage, error) {
	info, err := a.resolve(ctx, model)
	if err != nil {
		return "", "", tokenUsage{}, err
	}
	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(info.ID),
		MaxTokens: min(info.MaxTokens, apiMaxTokens),
		Messages:  []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock(prompt))},
	}
	if effortSupported(info, effort) {
		params.OutputConfig = anthropic.OutputConfigParam{Effort: anthropic.OutputConfigEffort(effort)}
	}
	stream := a.client.Messages.NewStreaming(ctx, params)
	var msg anthropic.Message
	for stream.Next() {
		if err := msg.Accumulate(stream.Current()); err != nil {
			return "", "", messageUsage(msg), fmt.Errorf("model %s: reading the response: %w", info.ID, err)
		}
	}
	if err := stream.Err(); err != nil {
		return "", "", messageUsage(msg), fmt.Errorf("model %s: %w", info.ID, err)
	}
	if msg.StopReason == anthropic.StopReasonRefusal || msg.StopReason == anthropic.StopReasonMaxTokens {
		return "", "", messageUsage(msg), fmt.Errorf("model %s: response ended with stop reason %q", info.ID, msg.StopReason)
	}
	var b strings.Builder
	for _, block := range msg.Content {
		if t, ok := block.AsAny().(anthropic.TextBlock); ok {
			b.WriteString(t.Text)
		}
	}
	return b.String(), string(msg.Model), messageUsage(msg), nil
}

// resolve returns the Models API record for model, from the cache after the
// first lookup. A full model id is fetched directly; any other name is taken
// for a Claude Code alias and matched against each model's line.
func (a *apiStructurer) resolve(ctx context.Context, model string) (anthropic.ModelInfo, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if info, ok := a.models[model]; ok {
		return info, nil
	}
	var (
		info anthropic.ModelInfo
		err  error
	)
	if strings.HasPrefix(model, "claude-") {
		var got *anthropic.ModelInfo
		got, err = a.client.Models.Get(ctx, model, anthropic.ModelGetParams{})
		if got != nil {
			info = *got
		}
	} else {
		info, err = a.newestOfLine(ctx, model)
	}
	if err != nil {
		return anthropic.ModelInfo{}, fmt.Errorf("resolving model %q: %w", model, err)
	}
	a.models[model] = info
	return info, nil
}

// newestOfLine returns the active model of line released last, the model a
// Claude Code alias of the same name stands for.
func (a *apiStructurer) newestOfLine(ctx context.Context, line string) (anthropic.ModelInfo, error) {
	var (
		best  anthropic.ModelInfo
		found bool
	)
	pager := a.client.Models.ListAutoPaging(ctx, anthropic.ModelListParams{})
	for pager.Next() {
		m := pager.Current()
		if string(m.Line) != line || m.Lifecycle != anthropic.ModelInfoLifecycleActive {
			continue
		}
		if !found || m.CreatedAt.After(best.CreatedAt) {
			best, found = m, true
		}
	}
	if err := pager.Err(); err != nil {
		return anthropic.ModelInfo{}, err
	}
	if !found {
		return anthropic.ModelInfo{}, fmt.Errorf("no active model of line %q", line)
	}
	return best, nil
}

// effortSupported reports whether info accepts effort as output_config.effort.
// A model without the capability, or without that level, is called without
// it, as a Claude Code session on that model runs.
func effortSupported(info anthropic.ModelInfo, effort string) bool {
	e := info.Capabilities.Effort
	levels := [...]anthropic.CapabilitySupport{e.Low, e.Medium, e.High, e.Xhigh, e.Max} // in the order of efforts
	i := slices.Index(efforts, effort)
	return e.Supported && i >= 0 && levels[i].Supported
}

// messageUsage converts a message's usage into the accumulator's counts.
func messageUsage(msg anthropic.Message) tokenUsage {
	return tokenUsage{
		InputTokens:         msg.Usage.InputTokens,
		OutputTokens:        msg.Usage.OutputTokens,
		CacheReadTokens:     msg.Usage.CacheReadInputTokens,
		CacheCreationTokens: msg.Usage.CacheCreationInputTokens,
	}
}
