package main

import (
	"testing"

	"github.com/planwerk/planwerk-review/internal/cli"
)

// TestResolveCaptureWiki locks the precedence of the capture write-back gate:
// an explicit --capture-wiki flag, then PLANWERK_CAPTURE_WIKI, then the config
// file's capture.wiki, then default-off. boolPtr is shared with
// resolve_wiki_test.go.
func TestResolveCaptureWiki(t *testing.T) {
	t.Run("explicit flag beats env and config", func(t *testing.T) {
		t.Setenv(envCaptureWiki, "false")
		if !resolveCaptureWiki(true, true, cli.CaptureFileConfig{Wiki: boolPtr(false)}) {
			t.Error("--capture-wiki=true must beat a falsy env var and config")
		}
	})

	t.Run("explicit flag false beats truthy env and config", func(t *testing.T) {
		t.Setenv(envCaptureWiki, "true")
		if resolveCaptureWiki(false, true, cli.CaptureFileConfig{Wiki: boolPtr(true)}) {
			t.Error("--capture-wiki=false must beat a truthy env var and config")
		}
	})

	t.Run("env beats config when no flag is set", func(t *testing.T) {
		t.Setenv(envCaptureWiki, "true")
		if !resolveCaptureWiki(false, false, cli.CaptureFileConfig{Wiki: boolPtr(false)}) {
			t.Error("PLANWERK_CAPTURE_WIKI=true must enable, beating config wiki:false")
		}
	})

	t.Run("config used when no flag or env", func(t *testing.T) {
		if !resolveCaptureWiki(false, false, cli.CaptureFileConfig{Wiki: boolPtr(true)}) {
			t.Error("capture.wiki:true must enable the write-back")
		}
	})

	t.Run("default off", func(t *testing.T) {
		if resolveCaptureWiki(false, false, cli.CaptureFileConfig{}) {
			t.Error("the write-back must be off by default with no flag, env, or config")
		}
	})
}
