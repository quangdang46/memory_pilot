// Planner: orders candidates by expected benefit per unit of cost+risk
// (bead 3r5). Pure function; rate limits and breakers are inputs.
package policy

import (
	"sort"

	"memory_pilot/internal/model"
)

type PlannerCost struct {
	Broken map[model.ActionKind]bool
}

// Plan orders candidates: benefit (expected bytes) per unit (cost+risk+1),
// broken kinds dropped entirely.
func Plan(candidates []model.Candidate, broken map[model.ActionKind]bool) []model.Candidate {
	out := make([]model.Candidate, 0, len(candidates))
	for _, c := range candidates {
		if broken[c.Kind] {
			continue
		}
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return benefitPerCost(out[i]) > benefitPerCost(out[j])
	})
	return out
}

func benefitPerCost(c model.Candidate) float64 {
	cost := 1.0 // self-actions cost ~nothing; L2 carries implicit risk weight
	if c.Kind == model.ActionStopOwned {
		cost = 8.0 // deliberately expensive: scarce, risky, last resort
	}
	if c.Kind == model.ActionEmitAdvisory {
		cost = 0.5
	}
	b := float64(c.ExpectBytes)
	if b <= 0 {
		b = 1 // advisory-type value normalized
	}
	return b / cost
}
