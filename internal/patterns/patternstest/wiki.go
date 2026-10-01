package patternstest

import "github.com/planwerk/planwerk-agent/internal/patterns"

// MemoryWiki is a resolved wiki that carries MemoryPage and nothing else.
func MemoryWiki() patterns.ResolvedWiki {
	return patterns.ResolvedWiki{Repo: "owner/repo", CommitSHA: "wikisha", MemoryPages: []patterns.MemoryPage{MemoryPage()}}
}

// WikiSeam is a ResolveWiki seam that yields Wiki. It counts its calls, each
// of which stands for a wiki clone or refresh the run pays for, and records
// what the last one was given: Repo is the "owner/name" it named.
type WikiSeam struct {
	Wiki   patterns.ResolvedWiki
	Calls  int
	Repo   string
	Opts   patterns.WikiOptions
	Remote patterns.RemoteOptions
}

// Resolve has the signature of patterns.ResolveWiki.
func (s *WikiSeam) Resolve(owner, name string, wopts patterns.WikiOptions, ropts patterns.RemoteOptions) patterns.ResolvedWiki {
	s.Calls++
	s.Repo, s.Opts, s.Remote = owner+"/"+name, wopts, ropts
	return s.Wiki
}
