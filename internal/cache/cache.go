package cache

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/planwerk/planwerk-review/internal/report"
)

// Command names used to scope cache operations and identify envelope origin.
const (
	CommandReview   = "review"
	CommandPropose  = "propose"
	CommandAudit    = "audit"
	CommandGlossary = "glossary"
)

// DefaultMaxAge is the default TTL for cached entries. Older entries are
// treated as cache misses so a stale review or audit cannot be silently
// re-rendered weeks later.
const DefaultMaxAge = 30 * 24 * time.Hour

var cacheDir = defaultCacheDir()

// now is the time source; tests replace it to exercise age-based logic.
var now = time.Now

func defaultCacheDir() string {
	if dir, err := os.UserCacheDir(); err == nil {
		return filepath.Join(dir, "planwerk-review")
	}
	return filepath.Join(os.TempDir(), "planwerk-review-cache")
}

// SetDir overrides the cache directory and returns a function that restores
// the previous value. It is intended for use in tests; callers should invoke
// the restore function (e.g. via t.Cleanup) so parallel tests do not bleed
// state into each other.
func SetDir(dir string) (restore func()) {
	old := cacheDir
	cacheDir = dir
	return func() { cacheDir = old }
}

// SetNow overrides the cache time source and returns a function that restores
// the previous value. Intended for tests.
func SetNow(fn func() time.Time) (restore func()) {
	old := now
	now = fn
	return func() { now = old }
}

// envelope wraps every cached payload with the provenance metadata needed for
// TTL enforcement, scoped clearing, and inspection. Legacy files written before
// the envelope was introduced deserialize with zero-value fields and are
// treated as cache misses.
type envelope struct {
	WrittenAt time.Time       `json:"writtenAt"`
	Command   string          `json:"command"`
	Payload   json.RawMessage `json:"payload"`
}

// Metadata describes a single cached entry, used by Stats and Inspect.
type Metadata struct {
	Key       string        `json:"key"`
	Command   string        `json:"command"`
	WrittenAt time.Time     `json:"writtenAt"`
	Age       time.Duration `json:"age"`
	Size      int64         `json:"size"`
}

// CommandStats is a per-command breakdown returned by Stats.
type CommandStats struct {
	Count int   `json:"count"`
	Size  int64 `json:"size"`
}

// AgeBuckets groups entries by approximate age for human-readable stats.
type AgeBuckets struct {
	LessThanDay    int `json:"lessThanDay"`    // age <= 24h
	LessThanWeek   int `json:"lessThanWeek"`   // 24h < age <= 7d
	LessThanMonth  int `json:"lessThanMonth"`  // 7d < age <= 30d
	OlderThanMonth int `json:"olderThanMonth"` // age > 30d
}

// StatsResult summarizes the cache directory contents.
type StatsResult struct {
	Dir       string                  `json:"dir"`
	Total     int                     `json:"total"`
	TotalSize int64                   `json:"totalSize"`
	ByCommand map[string]CommandStats `json:"byCommand"`
	Ages      AgeBuckets              `json:"ages"`
	Oldest    *Metadata               `json:"oldest,omitempty"`
	Newest    *Metadata               `json:"newest,omitempty"`
	Entries   []Metadata              `json:"entries"`
}

// ErrNotFound is returned by Inspect when the key has no cache entry.
var ErrNotFound = errors.New("cache entry not found")

// Key generates a cache key from the PR head SHA and review flags.
func Key(owner, repo string, number int, headSHA string, flags ...string) string {
	input := fmt.Sprintf("%s/%s#%d@%s", owner, repo, number, headSHA)
	for _, f := range flags {
		input += "+" + f
	}
	h := sha256.Sum256([]byte(input))
	return fmt.Sprintf("%x", h[:16])
}

// RepoKey generates a cache key for repository analysis.
// It includes the HEAD SHA so the cache invalidates when the repo changes,
// plus any flags that alter the analysis output (e.g. the resolved wiki commit)
// so a moving wiki busts the cache rather than serving a stale proposal.
func RepoKey(owner, repo, headSHA string, flags ...string) string {
	input := fmt.Sprintf("propose:%s/%s@%s", owner, repo, headSHA)
	for _, f := range flags {
		input += "+" + f
	}
	h := sha256.Sum256([]byte(input))
	return fmt.Sprintf("%x", h[:16])
}

// GlossaryKey generates a cache key for a generated domain glossary. It
// includes the default-branch HEAD SHA so the cache invalidates when the repo
// changes. The "glossary:" prefix keeps it disjoint from RepoKey's "propose:"
// space so a propose and a glossary run on the same repo+SHA never collide.
func GlossaryKey(owner, repo, headSHA string) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("glossary:%s/%s@%s", owner, repo, headSHA)))
	return fmt.Sprintf("%x", h[:16])
}

// AuditKey generates a cache key for a full-codebase audit.
// It includes the default-branch HEAD SHA so the cache invalidates when the
// repo changes, plus any flags that alter the audit output.
func AuditKey(owner, repo, headSHA string, flags ...string) string {
	input := fmt.Sprintf("audit:%s/%s@%s", owner, repo, headSHA)
	for _, f := range flags {
		input += "+" + f
	}
	h := sha256.Sum256([]byte(input))
	return fmt.Sprintf("%x", h[:16])
}

// Get retrieves a cached review result if it exists and is within maxAge.
// A maxAge of 0 disables the age check.
func Get(key string, maxAge time.Duration) (*report.ReviewResult, bool) {
	payload, ok := readPayload(key, maxAge)
	if !ok {
		return nil, false
	}
	var result report.ReviewResult
	if err := json.Unmarshal(payload, &result); err != nil {
		return nil, false
	}
	return &result, true
}

// Put stores a review result in the cache under the given command scope.
func Put(key, command string, result *report.ReviewResult) error {
	data, err := json.Marshal(result)
	if err != nil {
		return err
	}
	return writeEnvelope(key, command, data)
}

// GetRaw retrieves the raw cached payload if it exists and is within maxAge.
// A maxAge of 0 disables the age check.
func GetRaw(key string, maxAge time.Duration) ([]byte, bool) {
	payload, ok := readPayload(key, maxAge)
	if !ok {
		return nil, false
	}
	// Return a copy so callers can't accidentally mutate the cached bytes.
	out := make([]byte, len(payload))
	copy(out, payload)
	return out, true
}

// PutRaw stores a raw payload in the cache under the given command scope.
func PutRaw(key, command string, data []byte) error {
	return writeEnvelope(key, command, data)
}

func writeEnvelope(key, command string, payload []byte) error {
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		return err
	}
	env := envelope{
		WrittenAt: now().UTC(),
		Command:   command,
		Payload:   json.RawMessage(payload),
	}
	data, err := json.Marshal(env)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(cacheDir, key+".json"), data, 0o600)
}

func readPayload(key string, maxAge time.Duration) ([]byte, bool) {
	env, ok := readEnvelope(key)
	if !ok {
		return nil, false
	}
	if env.WrittenAt.IsZero() || len(env.Payload) == 0 {
		// Legacy or malformed entry — treat as cache miss.
		return nil, false
	}
	if maxAge > 0 {
		age := now().Sub(env.WrittenAt)
		if age > maxAge {
			slog.Info("cache entry exceeds max age — ignoring",
				"key", key,
				"command", env.Command,
				"age", age.Round(time.Second),
				"max-age", maxAge)
			return nil, false
		}
	}
	return env.Payload, true
}

func readEnvelope(key string) (envelope, bool) {
	path := filepath.Join(cacheDir, key+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return envelope{}, false
	}
	var env envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return envelope{}, false
	}
	return env, true
}

// Clear removes cached entries. When command is empty, every entry is removed;
// otherwise only entries whose envelope matches command are deleted. Legacy
// (pre-envelope) files are always removed when command is empty and skipped
// when a scope is specified, since they carry no provenance to match against.
func Clear(command string) error {
	entries, err := os.ReadDir(cacheDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	removed := 0
	skipped := 0
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".json" {
			continue
		}
		path := filepath.Join(cacheDir, e.Name())
		if command != "" {
			key := e.Name()[:len(e.Name())-len(".json")]
			env, ok := readEnvelope(key)
			if !ok || env.Command != command {
				skipped++
				continue
			}
		}
		if err := os.Remove(path); err != nil {
			return err
		}
		removed++
	}
	attrs := []any{"count", removed}
	if command != "" {
		attrs = append(attrs, "scope", command, "skipped", skipped)
	}
	slog.Info("removed cached entries", attrs...)
	return nil
}

// Stats walks the cache directory and returns aggregated metrics plus a
// chronologically sorted list of entries (newest first).
func Stats() (StatsResult, error) {
	out := StatsResult{
		Dir:       cacheDir,
		ByCommand: map[string]CommandStats{},
	}
	entries, err := os.ReadDir(cacheDir)
	if os.IsNotExist(err) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	n := now()
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		key := e.Name()[:len(e.Name())-len(".json")]
		info, err := e.Info()
		if err != nil {
			continue
		}
		env, ok := readEnvelope(key)
		command := "unknown"
		writtenAt := time.Time{}
		if ok && !env.WrittenAt.IsZero() {
			writtenAt = env.WrittenAt
			if env.Command != "" {
				command = env.Command
			}
		}
		meta := Metadata{
			Key:       key,
			Command:   command,
			WrittenAt: writtenAt,
			Size:      info.Size(),
		}
		if !writtenAt.IsZero() {
			meta.Age = n.Sub(writtenAt)
		}
		out.Total++
		out.TotalSize += meta.Size
		cs := out.ByCommand[command]
		cs.Count++
		cs.Size += meta.Size
		out.ByCommand[command] = cs

		if !writtenAt.IsZero() {
			switch {
			case meta.Age <= 24*time.Hour:
				out.Ages.LessThanDay++
			case meta.Age <= 7*24*time.Hour:
				out.Ages.LessThanWeek++
			case meta.Age <= 30*24*time.Hour:
				out.Ages.LessThanMonth++
			default:
				out.Ages.OlderThanMonth++
			}
		} else {
			out.Ages.OlderThanMonth++
		}
		out.Entries = append(out.Entries, meta)
	}

	sort.Slice(out.Entries, func(i, j int) bool {
		return out.Entries[i].WrittenAt.After(out.Entries[j].WrittenAt)
	})
	if len(out.Entries) > 0 {
		newest := out.Entries[0]
		oldest := out.Entries[len(out.Entries)-1]
		out.Newest = &newest
		out.Oldest = &oldest
	}
	return out, nil
}

// Inspect returns the metadata and raw payload for a single cache key. It
// bypasses the max-age check so operators can always see what is stored.
func Inspect(key string) (Metadata, []byte, error) {
	path := filepath.Join(cacheDir, key+".json")
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return Metadata{}, nil, ErrNotFound
	}
	if err != nil {
		return Metadata{}, nil, err
	}
	env, ok := readEnvelope(key)
	if !ok {
		return Metadata{}, nil, fmt.Errorf("read cache entry %s: %w", key, errors.New("unreadable envelope"))
	}
	meta := Metadata{
		Key:       key,
		Command:   env.Command,
		WrittenAt: env.WrittenAt,
		Size:      info.Size(),
	}
	if meta.Command == "" {
		meta.Command = "unknown"
	}
	if !env.WrittenAt.IsZero() {
		meta.Age = now().Sub(env.WrittenAt)
	}
	return meta, env.Payload, nil
}
