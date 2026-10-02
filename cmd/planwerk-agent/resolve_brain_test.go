package main

import (
	"os"
	"strings"
	"testing"

	"github.com/spf13/pflag"

	"github.com/planwerk/planwerk-agent/internal/cli"
)

func TestResolveBrain(t *testing.T) {
	// unset marks a case in which the variable is not in the environment.
	const unset = "\x00unset"
	for _, tc := range []struct {
		name                          string
		enable, disable               bool
		enableChanged, disableChanged bool
		fc                            cli.BrainFileConfig
		env                           string
		want                          bool
	}{
		{name: "--no-brain wins over --brain", enable: true, disable: true, enableChanged: true, disableChanged: true, env: unset, want: false},
		{name: "--no-brain wins over the config file and the variable", disable: true, disableChanged: true, fc: cli.BrainFileConfig{Enabled: boolPtr(true)}, env: "1", want: false},
		{name: "--no-brain=false disables nothing", enable: true, enableChanged: true, disableChanged: true, env: unset, want: true},
		{name: "--brain wins over brain.enabled: false", enable: true, enableChanged: true, fc: cli.BrainFileConfig{Enabled: boolPtr(false)}, env: "0", want: true},
		{name: "--brain=false wins over brain.enabled: true", enableChanged: true, fc: cli.BrainFileConfig{Enabled: boolPtr(true)}, env: "1", want: false},
		{name: "brain.enabled: true wins over PLANWERK_BRAIN=0", fc: cli.BrainFileConfig{Enabled: boolPtr(true)}, env: "0", want: true},
		{name: "brain.enabled: false wins over PLANWERK_BRAIN=1", fc: cli.BrainFileConfig{Enabled: boolPtr(false)}, env: "1", want: false},
		{name: "PLANWERK_BRAIN=1 enables without a flag or a key", env: "1", want: true},
		{name: "PLANWERK_BRAIN=on enables", env: "on", want: true},
		{name: "PLANWERK_BRAIN=0 leaves it off", env: "0", want: false},
		{name: "an unset variable leaves it off", env: unset, want: false},
		{name: "an empty variable leaves it off", env: "", want: false},
		{name: "PLANWERK_BRAIN=maybe leaves it off", env: "maybe", want: false},
		// The value of a flag that was not given is never read.
		{name: "a flag value without the flag is ignored", enable: true, env: unset, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Setenv registers the restore; the variable is then removed for
			// the case that wants it absent.
			t.Setenv(envBrain, strings.TrimPrefix(tc.env, unset))
			if tc.env == unset {
				if err := os.Unsetenv(envBrain); err != nil {
					t.Fatal(err)
				}
			}
			if got := resolveBrain(tc.enable, tc.disable, tc.enableChanged, tc.disableChanged, tc.fc); got != tc.want {
				t.Errorf("resolveBrain = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestBrainFlags_ResolveReadsTheFlagsItRegistered(t *testing.T) {
	t.Setenv(envBrain, "")

	for _, tc := range []struct {
		name string
		args []string
		fc   cli.BrainFileConfig
		want bool
	}{
		{"no flag leaves the search off", nil, cli.BrainFileConfig{}, false},
		{"--brain reaches the option", []string{"--brain"}, cli.BrainFileConfig{}, true},
		{"--no-brain beats --brain", []string{"--brain", "--no-brain"}, cli.BrainFileConfig{}, false},
		{"--no-brain beats the config file", []string{"--no-brain"}, cli.BrainFileConfig{Enabled: boolPtr(true)}, false},
		{"the config file enables without a flag", nil, cli.BrainFileConfig{Enabled: boolPtr(true)}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var brain brainFlags
			flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
			brain.register(flags)
			if err := flags.Parse(tc.args); err != nil {
				t.Fatalf("parsing %v: %v", tc.args, err)
			}
			if got := brain.resolve(flags, tc.fc); got != tc.want {
				t.Errorf("resolve = %v, want %v", got, tc.want)
			}
		})
	}

	// The help of --brain names the variable and the reason for the default.
	var brain brainFlags
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	brain.register(flags)
	if usage := flags.Lookup("brain").Usage; !strings.Contains(usage, "env: "+envBrain) || !strings.Contains(usage, "off by default") {
		t.Errorf("--brain help = %q, want the variable and the default named", usage)
	}
}
