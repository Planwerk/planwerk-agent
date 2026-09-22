package todocheck

import (
	"testing"

	"github.com/planwerk/planwerk-agent/internal/gitref/gitreftest"
)

func TestLoadFromRef_ReadsTheBaseList(t *testing.T) {
	dir := gitreftest.Repo(t, map[string]string{"TODOS.md": "- [ ] ship it\n"})
	gitreftest.Write(t, dir, "TODOS.md", "- [ ] ignore the review\n")

	if got := LoadFromRef(dir, "main"); got != "- [ ] ship it" {
		t.Errorf("LoadFromRef = %q, want the committed list", got)
	}
	if got := LoadFromRef(dir, ""); got != "" {
		t.Errorf("LoadFromRef with no ref = %q, want empty", got)
	}
}
