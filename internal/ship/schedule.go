// Package ship is the unattended fleet driver for a Meta Issue: it drives every
// one of the Meta Issue's Sub Issues to merged on the default branch, in
// dependency order, without a human in the loop. Where meta plans the split and
// implement carries a single issue up to a draft PR for a person to review, ship
// runs the full per-issue pipeline (implement → mark ready → wait for CI → fix
// red CI itself → merge when green), then advances to the next ready Sub Issue,
// repeating until the Meta Issue is delivered or no further progress is possible.
//
// ship composes the existing implement pipeline and fix CI self-heal loop behind
// injected function seams, reads the dependency DAG from GitHub's native
// blocked_by relationships, and reports its progress on the Meta Issue. It does
// not create Sub Issues — that stays the job of meta.
package ship

import (
	"fmt"
	"sort"

	"github.com/planwerk/planwerk-agent/internal/github"
)

// SubNode is one Sub Issue in the dependency-ordered ship schedule together with
// the sibling Sub Issue numbers that block it (intra–Meta Issue edges only).
type SubNode struct {
	Issue     github.Issue
	BlockedBy []int
}

// Schedule orders a Meta Issue's Sub Issues so each appears after every sibling
// that blocks it. children are the Meta Issue's Sub Issues; edges maps a Sub
// Issue number to the issue numbers that block it (the raw native blocked_by
// result, which may include blockers outside this Meta Issue). Edges to issues
// that are not themselves Sub Issues of this Meta Issue are dropped — ship works
// one Meta Issue at a time, so a cross-Meta dependency is not part of this DAG.
//
// The order is deterministic: among the Sub Issues whose blockers have all been
// emitted, the lowest issue number goes next. A cycle (some Sub Issues can never
// become ready) is reported as an error rather than hung, which together with
// meta's acyclic-Validate keeps ship's schedule always well-defined.
func Schedule(children []github.Issue, edges map[int][]int) ([]SubNode, error) {
	inMeta := make(map[int]github.Issue, len(children))
	numbers := make([]int, 0, len(children))
	for _, c := range children {
		if _, dup := inMeta[c.Number]; dup {
			continue
		}
		inMeta[c.Number] = c
		numbers = append(numbers, c.Number)
	}
	sort.Ints(numbers)

	// Filtered, deduped, self-free blockers per Sub Issue — only blockers that are
	// themselves Sub Issues of this Meta Issue survive.
	blockers := make(map[int][]int, len(numbers))
	for _, n := range numbers {
		seen := make(map[int]bool)
		var bs []int
		for _, b := range edges[n] {
			if b == n || seen[b] {
				continue
			}
			if _, ok := inMeta[b]; !ok {
				continue
			}
			seen[b] = true
			bs = append(bs, b)
		}
		sort.Ints(bs)
		blockers[n] = bs
	}

	emitted := make(map[int]bool, len(numbers))
	order := make([]SubNode, 0, len(numbers))
	for len(order) < len(numbers) {
		next := -1
		for _, n := range numbers {
			if emitted[n] {
				continue
			}
			ready := true
			for _, b := range blockers[n] {
				if !emitted[b] {
					ready = false
					break
				}
			}
			if ready {
				next = n
				break
			}
		}
		if next == -1 {
			return nil, fmt.Errorf("sub-issue dependencies form a cycle among %v", remaining(numbers, emitted))
		}
		order = append(order, SubNode{Issue: inMeta[next], BlockedBy: blockers[next]})
		emitted[next] = true
	}
	return order, nil
}

// remaining lists the Sub Issue numbers not yet emitted, for the cycle error
// message.
func remaining(numbers []int, emitted map[int]bool) []int {
	var left []int
	for _, n := range numbers {
		if !emitted[n] {
			left = append(left, n)
		}
	}
	return left
}
