package routing

import "github.com/kninetimmy/orch/internal/manifest"

// Attempt records one prior routing attempt and its fate. Failed marks
// a meaningful failure that permanently retires the attempt's model
// (PRD §11: Orch does not repeatedly retry an underpowered model);
// Reason is the caller-supplied detail behind that failure.
type Attempt struct {
	Role      manifest.Role
	Selection manifest.Selection
	Failed    bool
	Reason    string
}

// History is the ordered list of prior attempts for a task. A nil
// History is a fresh task with nothing retired. Both entry points take
// it as a required positional argument, and Escalate returns the
// updated History with the retired attempt appended — so the correct
// input to a second call is the first call's output.
type History []Attempt

// FailedModel reports whether any recorded attempt on model failed. The
// match is on the model string only: an effort bump on a model that has
// already failed is still a retry of that model, never a fresh route,
// so routing never offers it again (PRD §11).
func (h History) FailedModel(model string) bool {
	for _, a := range h {
		if a.Failed && a.Selection.Model == model {
			return true
		}
	}
	return false
}
