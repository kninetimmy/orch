package gitops

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
)

type inspectionStepContext struct {
	context.Context
	step, at int
	change   func() error
}

func (c *inspectionStepContext) Err() error {
	c.step++
	if c.step == c.at {
		return c.change()
	}
	return c.Context.Err()
}

func TestInspectWorktreeRefusesChangingDirectoryIdentities(t *testing.T) {
	for _, child := range []bool{false, true} {
		t.Run(map[bool]string{false: "root", true: "child"}[child], func(t *testing.T) {
			target := filepath.Join(t.TempDir(), "checkout")
			if err := os.Mkdir(target, 0o755); err != nil {
				t.Fatal(err)
			}
			changing := target
			if child {
				changing = filepath.Join(target, "child")
				if err := os.Mkdir(changing, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			// Change identity after the inspected handle/path pair is established,
			// at the walk's cancellation check. No production filesystem hooks.
			at := 2
			if runtime.GOOS == "windows" {
				for parent := filepath.Dir(target); ; parent = filepath.Dir(parent) {
					at += 2 // Initial parent evidence and pre-walk identity recheck.
					if filepath.Dir(parent) == parent {
						break
					}
				}
			}
			if child {
				at += 2 // Parent entry check, then the opened child's walk.
			}
			pinned := false
			ctx := &inspectionStepContext{Context: t.Context(), at: at, change: func() error {
				if err := os.Rename(changing, changing+"-saved"); err != nil {
					// Go's Windows OpenRoot handle denies root renames while
					// open. Accept only that precise sharing-violation outcome,
					// not an arbitrary failure to exercise the replacement.
					if runtime.GOOS == "windows" && !child && errors.Is(err, syscall.Errno(32)) {
						pinned = true
						return nil
					}
					t.Fatalf("replace owned directory identity: %v", err)
				}
				if err := os.Mkdir(changing, 0o755); err != nil {
					t.Fatal(err)
				}
				return nil
			}}
			err := InspectWorktree(ctx, target)
			if pinned {
				if err != nil || ctx.step < at {
					t.Fatalf("unchanged pinned root: %v", err)
				}
				t.Log("Go OpenRoot pins the Windows root during inspection; child replacement separately exercises identity-change refusal")
				return
			}
			if ctx.step < at || err == nil || !strings.Contains(err.Error(), "identity changed") || !strings.Contains(err.Error(), target) {
				t.Fatalf("changed directory accepted: step=%d at=%d err=%v", ctx.step, at, err)
			}
		})
	}
}
