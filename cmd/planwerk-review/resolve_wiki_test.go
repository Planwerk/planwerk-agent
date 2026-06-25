package main

import (
	"testing"

	"github.com/planwerk/planwerk-review/internal/cli"
)

func boolPtr(b bool) *bool    { return &b }
func strPtr(s string) *string { return &s }

func TestResolveWikiOptions_EnabledPrecedence(t *testing.T) {
	t.Run("--no-wiki overrides --wiki", func(t *testing.T) {
		// Both flags changed: --no-wiki wins.
		got := resolveWikiOptions(true, true, true, true, "", false, cli.WikiFileConfig{})
		if got.Enabled {
			t.Error("--no-wiki must disable the wiki even when --wiki is also set")
		}
	})

	t.Run("explicit --wiki beats env and config", func(t *testing.T) {
		t.Setenv(envWiki, "false")
		got := resolveWikiOptions(true, false, true, false, "", false, cli.WikiFileConfig{Enabled: boolPtr(false)})
		if !got.Enabled {
			t.Error("explicit --wiki=true must beat a falsy env var and config")
		}
	})

	t.Run("env beats config when no flag is set", func(t *testing.T) {
		t.Setenv(envWiki, "false")
		got := resolveWikiOptions(true, false, false, false, "", false, cli.WikiFileConfig{Enabled: boolPtr(true)})
		if got.Enabled {
			t.Error("PLANWERK_WIKI=false must disable the wiki, beating config enabled:true")
		}
	})

	t.Run("config disables when no flag or env is set", func(t *testing.T) {
		got := resolveWikiOptions(true, false, false, false, "", false, cli.WikiFileConfig{Enabled: boolPtr(false)})
		if got.Enabled {
			t.Error("config enabled:false must disable the wiki")
		}
	})

	t.Run("config enables when no flag or env is set", func(t *testing.T) {
		got := resolveWikiOptions(true, false, false, false, "", false, cli.WikiFileConfig{Enabled: boolPtr(true)})
		if !got.Enabled {
			t.Error("config enabled:true must opt the wiki in")
		}
	})

	t.Run("default is disabled", func(t *testing.T) {
		got := resolveWikiOptions(true, false, false, false, "", false, cli.WikiFileConfig{})
		if got.Enabled {
			t.Error("the wiki must be off by default with no flag, env, or config")
		}
	})
}

func TestResolveWikiOptions_RefPrecedence(t *testing.T) {
	t.Run("--wiki-ref beats env and config", func(t *testing.T) {
		t.Setenv(envWikiRef, "env-ref")
		got := resolveWikiOptions(true, false, false, false, "flag-ref", true, cli.WikiFileConfig{Ref: strPtr("cfg-ref")})
		if got.Ref != "flag-ref" {
			t.Errorf("Ref = %q, want flag-ref", got.Ref)
		}
	})

	t.Run("env beats config", func(t *testing.T) {
		t.Setenv(envWikiRef, "env-ref")
		got := resolveWikiOptions(true, false, false, false, "", false, cli.WikiFileConfig{Ref: strPtr("cfg-ref")})
		if got.Ref != "env-ref" {
			t.Errorf("Ref = %q, want env-ref", got.Ref)
		}
	})

	t.Run("config used when no flag or env", func(t *testing.T) {
		got := resolveWikiOptions(true, false, false, false, "", false, cli.WikiFileConfig{Ref: strPtr("cfg-ref")})
		if got.Ref != "cfg-ref" {
			t.Errorf("Ref = %q, want cfg-ref", got.Ref)
		}
	})
}

func TestResolveWikiOptions_RepoFromConfig(t *testing.T) {
	fc := cli.WikiFileConfig{Repo: strPtr(testRepoRef)}
	got := resolveWikiOptions(true, false, false, false, "", false, fc)
	if got.Repo != testRepoRef {
		t.Errorf("Repo = %q, want acme/widgets", got.Repo)
	}
}

func TestLookupBoolEnv(t *testing.T) {
	t.Run("unset is not ok", func(t *testing.T) {
		if _, ok := lookupBoolEnv("PLANWERK_WIKI_TEST_UNSET"); ok {
			t.Error("an unset variable must report ok=false")
		}
	})

	for _, raw := range []string{"1", "true", "TRUE", "yes", "On"} {
		t.Run("truthy-"+raw, func(t *testing.T) {
			t.Setenv("PLANWERK_WIKI_TEST_BOOL", raw)
			v, ok := lookupBoolEnv("PLANWERK_WIKI_TEST_BOOL")
			if !ok || !v {
				t.Errorf("%q should parse as (true, true), got (%v, %v)", raw, v, ok)
			}
		})
	}

	for _, raw := range []string{"0", "false", "no", "OFF"} {
		t.Run("falsy-"+raw, func(t *testing.T) {
			t.Setenv("PLANWERK_WIKI_TEST_BOOL", raw)
			v, ok := lookupBoolEnv("PLANWERK_WIKI_TEST_BOOL")
			if !ok || v {
				t.Errorf("%q should parse as (false, true), got (%v, %v)", raw, v, ok)
			}
		})
	}

	for _, raw := range []string{"", "garbage", "maybe"} {
		t.Run("unrecognized-"+raw, func(t *testing.T) {
			t.Setenv("PLANWERK_WIKI_TEST_BOOL", raw)
			if _, ok := lookupBoolEnv("PLANWERK_WIKI_TEST_BOOL"); ok {
				t.Errorf("%q should report ok=false so the caller falls through", raw)
			}
		})
	}
}
