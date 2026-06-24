package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"

	"go.yaml.in/yaml/v3"
)

// DefaultConfigPath is the relative path where planwerk-review looks for
// its repo-level configuration file.
const DefaultConfigPath = ".planwerk/config.yaml"

// FileConfig is the parsed representation of .planwerk/config.yaml. Pointer
// fields distinguish "absent" (nil) from "set to zero", which matters for
// ints like max-patterns where 0 is a meaningful value.
type FileConfig struct {
	Review  ReviewFileConfig  `yaml:"review"`
	Propose ProposeFileConfig `yaml:"propose"`
	Audit   AuditFileConfig   `yaml:"audit"`
	Wiki    WikiFileConfig    `yaml:"wiki"`
}

// WikiFileConfig is the top-level `wiki:` section of .planwerk/config.yaml. It
// configures the target repo's GitHub Wiki as a knowledge source for review,
// audit, propose, and the implement plan step. Pointer fields distinguish
// "absent" (nil) from "set", which matters for Enabled where the resolver must
// honor an explicit true or false over the default-off behavior.
type WikiFileConfig struct {
	Enabled *bool   `yaml:"enabled"`
	Repo    *string `yaml:"repo"`
	Ref     *string `yaml:"ref"`
}

type ReviewFileConfig struct {
	Patterns    []string `yaml:"patterns"`
	MinSeverity *string  `yaml:"min-severity"`
	Format      *string  `yaml:"format"`
	MaxPatterns *int     `yaml:"max-patterns"`
	MaxFindings *int     `yaml:"max-findings"`
}

type ProposeFileConfig struct {
	Patterns    []string `yaml:"patterns"`
	Format      *string  `yaml:"format"`
	MaxPatterns *int     `yaml:"max-patterns"`
}

type AuditFileConfig struct {
	Patterns         []string `yaml:"patterns"`
	MinSeverity      *string  `yaml:"min-severity"`
	IssueMinSeverity *string  `yaml:"issue-min-severity"`
	Format           *string  `yaml:"format"`
	MaxPatterns      *int     `yaml:"max-patterns"`
	MaxFindings      *int     `yaml:"max-findings"`
}

// LoadFileConfig reads the YAML config at path. A missing file is not an
// error — the second return value is false and err is nil. A malformed file
// (bad YAML or unknown keys) returns an error so CLI startup fails loudly
// instead of silently running with defaults that differ from user intent.
func LoadFileConfig(path string) (FileConfig, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return FileConfig{}, false, nil
		}
		return FileConfig{}, false, fmt.Errorf("read config %s: %w", path, err)
	}
	var cfg FileConfig
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		if errors.Is(err, io.EOF) {
			// Empty file — treat as present but with no overrides.
			return FileConfig{}, true, nil
		}
		return FileConfig{}, false, fmt.Errorf("parse config %s: %w", path, err)
	}
	return cfg, true, nil
}

// ApplyReview writes file-config defaults into cfg for any flag that was not
// changed on the command line. minSeverity receives the raw severity string
// so the caller can parse it with the existing flow. max-patterns is left to
// the caller's resolver because it also folds in the PLANWERK_MAX_PATTERNS
// environment variable.
func (f FileConfig) ApplyReview(cfg *Config, minSeverity *string, flagChanged func(string) bool) {
	r := f.Review
	if len(r.Patterns) > 0 && !flagChanged("patterns") {
		cfg.PatternDirs = append([]string(nil), r.Patterns...)
	}
	if r.MinSeverity != nil && !flagChanged("min-severity") {
		*minSeverity = *r.MinSeverity
	}
	if r.Format != nil && !flagChanged("format") {
		cfg.Format = *r.Format
	}
	if r.MaxFindings != nil && !flagChanged("max-findings") {
		cfg.MaxFindings = *r.MaxFindings
	}
}

// ApplyPropose writes file-config defaults into cfg for any flag that was not
// changed on the command line. max-patterns is handled by the caller.
func (f FileConfig) ApplyPropose(cfg *ProposeConfig, flagChanged func(string) bool) {
	p := f.Propose
	if len(p.Patterns) > 0 && !flagChanged("patterns") {
		cfg.PatternDirs = append([]string(nil), p.Patterns...)
	}
	if p.Format != nil && !flagChanged("format") {
		cfg.Format = *p.Format
	}
}

// ApplyAudit writes file-config defaults into cfg for any flag that was not
// changed on the command line. max-patterns is handled by the caller.
func (f FileConfig) ApplyAudit(cfg *AuditConfig, minSeverity, issueMinSeverity *string, flagChanged func(string) bool) {
	a := f.Audit
	if len(a.Patterns) > 0 && !flagChanged("patterns") {
		cfg.PatternDirs = append([]string(nil), a.Patterns...)
	}
	if a.MinSeverity != nil && !flagChanged("min-severity") {
		*minSeverity = *a.MinSeverity
	}
	if a.IssueMinSeverity != nil && !flagChanged("issue-min-severity") {
		*issueMinSeverity = *a.IssueMinSeverity
	}
	if a.Format != nil && !flagChanged("format") {
		cfg.Format = *a.Format
	}
	if a.MaxFindings != nil && !flagChanged("max-findings") {
		cfg.MaxFindings = *a.MaxFindings
	}
}
