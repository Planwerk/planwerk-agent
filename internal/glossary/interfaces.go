package glossary

// GenerateContext is the input to the glossary-generation prompt. The
// vocabulary is extracted from the checkout itself, so the only field the
// prompt needs is RepoName, which seeds the "# {Context Name}" heading hint so
// the generated CONTEXT.md is named after the repository rather than a generic
// placeholder.
type GenerateContext struct {
	RepoName string
}
