package instructions

import (
	"fmt"
	"strings"
)

// diffContext is the number of unchanged lines unifiedDiff keeps on
// each side of a change, and the distance within which two changes'
// context windows are merged into a single hunk.
const diffContext = 3

// noNewlineMarker is GNU diff's marker for a printed line that is the
// last line of its file and that file has no trailing newline.
const noNewlineMarker = `\ No newline at end of file`

// diffOpKind is the kind of one line-level edit in a Myers edit
// script.
type diffOpKind int

const (
	opEqual diffOpKind = iota
	opDelete
	opInsert
)

// diffOp is one step of an edit script transforming a into b. aIdx is
// the 0-based index into a's lines (meaningful for opEqual/opDelete);
// bIdx is the 0-based index into b's lines (meaningful for
// opEqual/opInsert).
type diffOp struct {
	kind       diffOpKind
	aIdx, bIdx int
}

// myersDiff returns the shortest edit script transforming a into b, as
// a sequence of per-line operations, using the greedy Myers algorithm's
// classic full-V-array variant: it keeps every depth's V array (O(D^2)
// space) rather than the linear-space Hirschberg refinement. That is
// fine here — every diff this package computes is over one managed
// block plus a little surrounding context, so D is tiny; a
// pathological input would be slow, never wrong. Ties in the textbook
// tie-break condition (k == -d || (k != d && v[k-1] < v[k+1])) are
// resolved identically on every call, so the output is deterministic.
func myersDiff(a, b []string) []diffOp {
	n, m := len(a), len(b)
	max := n + m
	if max == 0 {
		return nil
	}
	offset := max
	v := make([]int, 2*max+1)
	var trace [][]int
	depth := -1

findLoop:
	for d := 0; d <= max; d++ {
		snapshot := make([]int, len(v))
		copy(snapshot, v)
		trace = append(trace, snapshot)
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[k-1+offset] < v[k+1+offset]) {
				x = v[k+1+offset]
			} else {
				x = v[k-1+offset] + 1
			}
			y := x - k
			for x < n && y < m && a[x] == b[y] {
				x++
				y++
			}
			v[k+offset] = x
			if x >= n && y >= m {
				depth = d
				break findLoop
			}
		}
	}
	if depth < 0 {
		// Unreachable: d == max always exhausts both a and b for some
		// k, since max == n+m is the longest possible edit script.
		depth = max
	}
	return backtrack(a, b, trace, depth, offset)
}

// backtrack replays trace (each depth's V array, snapshotted before
// that depth's own updates) from the end of a and b back to the
// start, then reverses the result into forward order. Each step is
// either a run of diagonal "equal" moves (a snake) or a single
// insert/delete move.
func backtrack(a, b []string, trace [][]int, depth, offset int) []diffOp {
	x, y := len(a), len(b)
	var ops []diffOp
	for d := depth; d >= 0; d-- {
		v := trace[d]
		k := x - y
		var prevK int
		if k == -d || (k != d && v[k-1+offset] < v[k+1+offset]) {
			prevK = k + 1
		} else {
			prevK = k - 1
		}
		prevX := v[prevK+offset]
		prevY := prevX - prevK

		for x > prevX && y > prevY {
			x--
			y--
			ops = append(ops, diffOp{kind: opEqual, aIdx: x, bIdx: y})
		}
		if d > 0 {
			if x == prevX {
				y--
				ops = append(ops, diffOp{kind: opInsert, bIdx: y})
			} else {
				x--
				ops = append(ops, diffOp{kind: opDelete, aIdx: x})
			}
		}
		x, y = prevX, prevY
	}
	for i, j := 0, len(ops)-1; i < j; i, j = i+1, j-1 {
		ops[i], ops[j] = ops[j], ops[i]
	}
	return ops
}

// splitDiffLines splits s into its lines with no line terminators, and
// reports whether the final line is newline-terminated. Empty content
// yields no lines and a vacuous true (there is no final line to be
// missing a newline).
func splitDiffLines(s string) (lines []string, terminated bool) {
	if s == "" {
		return nil, true
	}
	terminated = strings.HasSuffix(s, "\n")
	trimmed := s
	if terminated {
		trimmed = s[:len(s)-1]
	}
	return strings.Split(trimmed, "\n"), terminated
}

// hunk is a contiguous range [start, end) of op indices: the unchanged
// context plus the changes it surrounds.
type hunk struct {
	start, end int
}

// buildHunks groups ops into hunks: each maximal run of non-equal ops
// is expanded by up to context equal ops on either side, and any two
// expanded ranges that touch or overlap are merged into one hunk.
func buildHunks(ops []diffOp, context int) []hunk {
	var changes [][2]int
	i := 0
	for i < len(ops) {
		if ops[i].kind == opEqual {
			i++
			continue
		}
		j := i
		for j < len(ops) && ops[j].kind != opEqual {
			j++
		}
		changes = append(changes, [2]int{i, j})
		i = j
	}
	if len(changes) == 0 {
		return nil
	}

	var hunks []hunk
	for _, c := range changes {
		start := c[0] - context
		if start < 0 {
			start = 0
		}
		end := c[1] + context
		if end > len(ops) {
			end = len(ops)
		}
		if len(hunks) > 0 && start <= hunks[len(hunks)-1].end {
			hunks[len(hunks)-1].end = end
		} else {
			hunks = append(hunks, hunk{start: start, end: end})
		}
	}
	return hunks
}

// prefixCounts returns, for every op index i, the number of ops in
// ops[:i] that consume an old line (oldPrefix, opEqual/opDelete) and a
// new line (newPrefix, opEqual/opInsert). Both slices have length
// len(ops)+1 so hunk boundaries — which are op indices, inclusive of
// len(ops) — index them directly.
func prefixCounts(ops []diffOp) (oldPrefix, newPrefix []int) {
	oldPrefix = make([]int, len(ops)+1)
	newPrefix = make([]int, len(ops)+1)
	for i, op := range ops {
		oldPrefix[i+1] = oldPrefix[i]
		newPrefix[i+1] = newPrefix[i]
		switch op.kind {
		case opEqual:
			oldPrefix[i+1]++
			newPrefix[i+1]++
		case opDelete:
			oldPrefix[i+1]++
		case opInsert:
			newPrefix[i+1]++
		}
	}
	return oldPrefix, newPrefix
}

// UnifiedDiff renders old and newText's line-level differences as
// unified-diff text; it is unifiedDiff's exported form, for
// internal/interview's configure-local summary, which needs a diff
// display surface over an entire proposed file rather than one
// Plan/PlanRemove-scoped Change. See unifiedDiff's doc comment for the
// exact format and its documented limitation.
func UnifiedDiff(old, newText string) string {
	return unifiedDiff(old, newText)
}

// unifiedDiff renders the line-level differences between old and
// newText as unified-diff text: 3 lines of context, overlapping-context
// hunks merged into one, 1-based "@@ -a,b +c,d @@" headers always
// carrying both the start and the count (never GNU's single-number
// abbreviation for a one-line range), and GNU's
// "\ No newline at end of file" marker after the last line of a side
// that has no trailing newline. It emits no "---"/"+++" header lines —
// this package has no path to name. old == newText returns "". Because
// the two inputs to every real call already agree byte-for-byte outside
// the region under diff, no CRLF handling is needed here: whatever
// line endings the inputs carry are compared and printed verbatim.
//
// Known limitation: line comparison ignores each line's own
// terminator, so two inputs whose lines are all textually identical
// but that differ only in whether the very last line carries a final
// "\n" also return "" — Diff == "" is documented as a one-way
// implication from Old == New (Change's doc comment), not an
// if-and-only-if, and no real Plan/PlanRemove splice produces this
// degenerate case (every real change also changes at least one line's
// text).
func unifiedDiff(old, newText string) string {
	if old == newText {
		return ""
	}
	oldLines, oldTerminated := splitDiffLines(old)
	newLines, newTerminated := splitDiffLines(newText)
	ops := myersDiff(oldLines, newLines)
	hunks := buildHunks(ops, diffContext)
	if len(hunks) == 0 {
		return ""
	}
	oldPrefix, newPrefix := prefixCounts(ops)

	var b strings.Builder
	for _, h := range hunks {
		writeHunk(&b, ops, h, oldLines, newLines, oldPrefix, newPrefix, oldTerminated, newTerminated)
	}
	return b.String()
}

// writeHunk writes one hunk's "@@ ... @@" header and body lines to b.
func writeHunk(b *strings.Builder, ops []diffOp, h hunk, oldLines, newLines []string, oldPrefix, newPrefix []int, oldTerminated, newTerminated bool) {
	oldCount := oldPrefix[h.end] - oldPrefix[h.start]
	newCount := newPrefix[h.end] - newPrefix[h.start]
	oldStart := oldPrefix[h.start] + 1
	if oldCount == 0 {
		oldStart = oldPrefix[h.start]
	}
	newStart := newPrefix[h.start] + 1
	if newCount == 0 {
		newStart = newPrefix[h.start]
	}
	fmt.Fprintf(b, "@@ -%d,%d +%d,%d @@\n", oldStart, oldCount, newStart, newCount)

	for _, op := range ops[h.start:h.end] {
		switch op.kind {
		case opEqual:
			b.WriteString(" ")
			b.WriteString(oldLines[op.aIdx])
			b.WriteByte('\n')
			lastOld := op.aIdx == len(oldLines)-1
			lastNew := op.bIdx == len(newLines)-1
			if (lastOld && !oldTerminated) || (lastNew && !newTerminated) {
				b.WriteString(noNewlineMarker)
				b.WriteByte('\n')
			}
		case opDelete:
			b.WriteString("-")
			b.WriteString(oldLines[op.aIdx])
			b.WriteByte('\n')
			if op.aIdx == len(oldLines)-1 && !oldTerminated {
				b.WriteString(noNewlineMarker)
				b.WriteByte('\n')
			}
		case opInsert:
			b.WriteString("+")
			b.WriteString(newLines[op.bIdx])
			b.WriteByte('\n')
			if op.bIdx == len(newLines)-1 && !newTerminated {
				b.WriteString(noNewlineMarker)
				b.WriteByte('\n')
			}
		}
	}
}
