package run

import (
	"fmt"

	"github.com/kninetimmy/orch/internal/metrics"
)

// recordMetric appends a metrics event for this run when metrics are
// enabled. It runs at the very end of a verb's success path, after
// every state/GitHub mutation, so a recorded event always describes a
// completed verb; a failure is a post-mutation error (PRD §21 is
// fail-closed like everything else — disable metrics to bypass a
// broken disk).
func (c *verbCtx) recordMetric(ev metrics.Event) error {
	if !c.cfg.Metrics.Enabled {
		return nil
	}
	ev.At = c.env.nowStamp()
	if err := metrics.Append(c.env.RepoRoot, c.st.Run.ID, ev); err != nil {
		return wrapAfterMutation(fmt.Errorf("record metrics: %w", err))
	}
	return nil
}
