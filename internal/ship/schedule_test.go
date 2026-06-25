package ship

import (
	"testing"

	"github.com/planwerk/planwerk-agent/internal/github"
)

func subs(numbers ...int) []github.Issue {
	var out []github.Issue
	for _, n := range numbers {
		out = append(out, github.Issue{Owner: "acme", Name: "widgets", Number: n, State: "open"})
	}
	return out
}

func order(nodes []SubNode) []int {
	out := make([]int, len(nodes))
	for i, n := range nodes {
		out[i] = n.Issue.Number
	}
	return out
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestSchedule_LinearChain(t *testing.T) {
	// 103 blocked by 102 blocked by 101 — must come out 101, 102, 103 regardless
	// of the children's input order.
	children := subs(103, 101, 102)
	edges := map[int][]int{103: {102}, 102: {101}}
	nodes, err := Schedule(children, edges)
	if err != nil {
		t.Fatalf("Schedule error: %v", err)
	}
	if got := order(nodes); !equalInts(got, []int{101, 102, 103}) {
		t.Fatalf("order = %v, want [101 102 103]", got)
	}
}

func TestSchedule_IndependentSiblingsByNumber(t *testing.T) {
	// No edges: independent siblings stay independently shippable, emitted in
	// ascending-number order for determinism.
	nodes, err := Schedule(subs(102, 101, 103), map[int][]int{})
	if err != nil {
		t.Fatalf("Schedule error: %v", err)
	}
	if got := order(nodes); !equalInts(got, []int{101, 102, 103}) {
		t.Fatalf("order = %v, want [101 102 103]", got)
	}
}

func TestSchedule_DropsEdgesOutsideMeta(t *testing.T) {
	// 102 is blocked by 999, which is not a Sub Issue of this Meta Issue. That
	// edge is dropped, so 102 is treated as unblocked.
	children := subs(101, 102)
	edges := map[int][]int{102: {999}}
	nodes, err := Schedule(children, edges)
	if err != nil {
		t.Fatalf("Schedule error: %v", err)
	}
	for _, n := range nodes {
		if n.Issue.Number == 102 && len(n.BlockedBy) != 0 {
			t.Fatalf("102 should have no in-meta blockers, got %v", n.BlockedBy)
		}
	}
	if got := order(nodes); !equalInts(got, []int{101, 102}) {
		t.Fatalf("order = %v, want [101 102]", got)
	}
}

func TestSchedule_CycleReported(t *testing.T) {
	// 101 -> 102 -> 101 is a cycle: Schedule must report it, not hang.
	children := subs(101, 102)
	edges := map[int][]int{101: {102}, 102: {101}}
	if _, err := Schedule(children, edges); err == nil {
		t.Fatalf("expected a cycle error, got nil")
	}
}

func TestSchedule_SelfEdgeIgnored(t *testing.T) {
	// A self-blocking edge is meaningless and must not produce a phantom cycle.
	nodes, err := Schedule(subs(101), map[int][]int{101: {101}})
	if err != nil {
		t.Fatalf("Schedule error: %v", err)
	}
	if got := order(nodes); !equalInts(got, []int{101}) {
		t.Fatalf("order = %v, want [101]", got)
	}
}

func TestSchedule_Empty(t *testing.T) {
	nodes, err := Schedule(nil, nil)
	if err != nil {
		t.Fatalf("Schedule error: %v", err)
	}
	if len(nodes) != 0 {
		t.Fatalf("order = %v, want empty", order(nodes))
	}
}
