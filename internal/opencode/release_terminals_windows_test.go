package opencode

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/kninetimmy/orch/internal/execx"
)

func TestWindowsReleaseTerminalWorkers(t *testing.T) {
	for _, outcome := range []string{"success", "failed read", "deadline on recheck"} {
		t.Run(outcome, func(t *testing.T) {
			f := newReleaseFixture(t)
			const count = 17
			sessions := make([]releaseSession, count)
			for i := range sessions {
				id := fmt.Sprintf("ses%02d", i)
				sessions[i] = releaseSession{ID: id, Location: releaseLocation{Directory: f.root}}
				f.bodies["/api/experimental/session/"+id+"/terminal"] = `{"data":[]}`
			}
			f.bodies["/api/session?limit=100"] = releaseJSON(map[string]any{"data": sessions, "cursor": struct{}{}})

			synctest.Test(t, func(t *testing.T) {
				start := time.Now()
				started, canceled := make(chan struct{}, count), make(chan struct{}, count)
				reply, allowJoin := make(chan struct{}), make(chan struct{})
				recheck := make(chan struct{})
				if outcome == "deadline on recheck" {
					f.beforeReinventory = func() {
						f.loaded = slices.DeleteFunc(f.loaded, func(key string) bool { return key == f.unrelated })
						// A simulated 90s of the original 120s elapse before recheck.
						<-time.After(90 * time.Second)
						close(recheck)
						f.beforeReinventory = nil
					}
				}
				f.beforeCall = func(ctx context.Context, cmd execx.Cmd) error {
					if len(cmd.Args) != 3 || !strings.HasSuffix(cmd.Args[2], "/terminal") ||
						(outcome == "deadline on recheck" && f.inventories < 2) {
						return nil
					}
					started <- struct{}{}
					select {
					case <-reply:
						if outcome == "failed read" {
							return errors.New("credential-token")
						}
						return nil
					case <-ctx.Done():
						canceled <- struct{}{}
						<-allowJoin
						// Even a nominally successful subprocess is not evidence after expiry.
						return nil
					}
				}
				done := make(chan error, 1)
				go func() { done <- f.run(t) }()
				if outcome == "deadline on recheck" {
					select {
					case <-recheck:
					case err := <-done:
						t.Fatalf("release did not recheck: %v", err)
					}
				}
				synctest.Wait()
				if len(started) != 8 || len(done) != 0 || f.deletes != 0 {
					close(reply)
					close(allowJoin)
					t.Fatalf("at barrier: started=%d (want 8), returned=%t, evictions=%d", len(started), len(done) != 0, f.deletes)
				}
				if outcome == "success" {
					close(reply)
					if err := <-done; err != nil || len(started) != count || f.deletes != 1 {
						t.Fatalf("complete page: err=%v, reads=%d, evictions=%d", err, len(started), f.deletes)
					}
					return
				}
				wantCanceled := 7
				if outcome == "failed read" {
					reply <- struct{}{}
				} else {
					<-canceled
					wantCanceled = 8 - 1 // One cancellation was used to await expiry.
				}
				synctest.Wait()
				if len(canceled) != wantCanceled || len(started) != 8 || len(done) != 0 || f.deletes != 0 {
					t.Errorf("before join: canceled=%d want %d, started=%d, returned=%t, evictions=%d",
						len(canceled), wantCanceled, len(started), len(done) != 0, f.deletes)
				}
				close(allowJoin)
				err := <-done
				if outcome == "deadline on recheck" {
					if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) != 2*time.Minute {
						t.Errorf("expired in %s with %v, want original two-minute deadline", time.Since(start), err)
					}
				} else if err == nil || !strings.Contains(err.Error(), "unavailable or unsupported") || strings.Contains(err.Error(), "credential-token") {
					t.Errorf("expected safe read failure, got %v", err)
				}
				if f.deletes != 0 || len(started) != 8 {
					t.Fatalf("failed page: unexpected reads=%d or evictions=%d", len(started), f.deletes)
				}
			})
		})
	}
}

func TestWindowsReleaseRejectsIncompleteTerminalEvidence(t *testing.T) {
	page := func(id, directory, next string) string {
		return releaseJSON(map[string]any{
			"data":   []releaseSession{{ID: id, Location: releaseLocation{Directory: directory}}},
			"cursor": map[string]string{"next": next},
		})
	}
	endpoint := func(cursor string) string {
		return "/api/session?" + url.Values{"cursor": {cursor}, "limit": {"100"}}.Encode()
	}
	const first = "/api/session?limit=100"
	const terminal = "/api/experimental/session/sesIdle/terminal"
	for _, tc := range []struct {
		name, want string
		arrange    func(*releaseFixture)
	}{
		{"cursor cycle with unique sessions", "did not advance", func(f *releaseFixture) {
			f.bodies[first] = page("sesIdle", f.root, "A")
			f.bodies[endpoint("A")] = page("sesA", f.root, "B")
			f.bodies["/api/experimental/session/sesA/terminal"] = `{"data":[]}`
			f.bodies[endpoint("B")] = page("sesB", f.root, "A")
		}},
		{"empty continuing page", "did not advance", func(f *releaseFixture) {
			f.bodies[first] = `{"data":[],"cursor":{"next":"A"}}`
		}},
		{"oversize session page", "malformed session/terminal inventory", func(f *releaseFixture) {
			f.bodies[first] = `{"data":[` + strings.Repeat(`{},`, 100) + `{}],"cursor":{}}`
		}},
		{"oversize terminal list", "malformed persistent-terminal inventory", func(f *releaseFixture) {
			f.bodies[terminal] = `{"data":[` + strings.Repeat(`{},`, 1000) + `{}]}`
		}},
		{"duplicate terminal row", "unsupported persistent-terminal evidence", func(f *releaseFixture) {
			entry := map[string]string{"id": "ptyDuplicate", "sessionID": "sesIdle", "status": "exited", "cwd": f.root}
			f.bodies[terminal] = releaseJSON(map[string]any{"data": []any{entry, entry}})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newReleaseFixture(t)
			tc.arrange(f)
			if err := f.run(t); err == nil || !strings.Contains(err.Error(), tc.want) || f.deletes != 0 {
				t.Fatalf("expected refusal before eviction: err=%v, deletes=%d", err, f.deletes)
			}
		})
	}
}
