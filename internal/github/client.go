package github

// Client is the one implementation of every GitHub and git operation the
// commands need. It is a stateless value: each method shells out to gh or git
// with its own timeout, so the zero value is ready to use, and one Client{}
// satisfies every command package's narrow GitHubClient interface
// structurally. The pure helpers (ParseRef, ParseDiff, SummarizeChecks, ...)
// stay package-level functions because they run no subprocess.
type Client struct{}
