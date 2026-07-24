package propose

// Proposal represents a single feature proposal for the repository.
type Proposal struct {
	ID                 string   `json:"id"`
	Priority           string   `json:"priority"` // HIGH, MEDIUM, LOW
	Category           string   `json:"category"` // feature, improvement, refactoring, testing, documentation, security, performance
	Title              string   `json:"title"`
	Description        string   `json:"description"`
	Motivation         string   `json:"motivation"`
	Scope              string   `json:"scope"` // Small, Medium, Large
	AffectedAreas      []string `json:"affected_areas"`
	AcceptanceCriteria []string `json:"acceptance_criteria"`
}

// ProposalResult holds all proposals plus a high-level overview of the analyzed repository.
type ProposalResult struct {
	RepositoryOverview string     `json:"repository_overview"`
	Proposals          []Proposal `json:"proposals"`
	// Model is the resolved Claude model id (e.g. "claude-opus-5") that
	// produced this result. It is threaded per-run to the attribution footer
	// and excluded from the serialized payload.
	Model string `json:"-"`
	// WikiRepo and WikiCommit record the target repo's GitHub Wiki and the
	// concrete commit its knowledge was resolved to, surfaced in the report
	// header for run-to-run reproducibility. Both are empty when no wiki was
	// used; threaded per-run and excluded from the cached payload.
	WikiRepo   string `json:"-"`
	WikiCommit string `json:"-"`
}

// CategorizedProposals groups proposals by priority.
type CategorizedProposals struct {
	High   []Proposal
	Medium []Proposal
	Low    []Proposal
}

// CategorizeByPriority groups proposals into HIGH, MEDIUM, LOW.
func CategorizeByPriority(proposals []Proposal) CategorizedProposals {
	var cp CategorizedProposals
	for _, p := range proposals {
		switch p.Priority {
		case "HIGH":
			cp.High = append(cp.High, p)
		case "MEDIUM":
			cp.Medium = append(cp.Medium, p)
		case "LOW":
			cp.Low = append(cp.Low, p)
		default:
			cp.Low = append(cp.Low, p)
		}
	}
	return cp
}
