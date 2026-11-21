package opencode

import (
	"context"
	"fmt"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
)

const windowsTerminalReads = 8

type releaseSession struct {
	ID       string          `json:"id"`
	Location releaseLocation `json:"location"`
}

// Persistent terminals can survive an idle session or a session move. Walk
// the unfiltered session inventory, not just sessions assigned to the targets.
func releaseActivity(ctx context.Context, request func(context.Context, string, string, any) error, targets []string) error {
	windows := runtime.GOOS == "windows"
	seenSessions, seenCursors := make(map[string]bool), make(map[string]bool)
	cursor := ""
	// Windows bounds the walk by the shared release deadline, not session age
	// or count. Elsewhere retain the sequential ten-page limit.
	for page := 0; windows || page < 10; page++ {
		var response struct {
			Data   []releaseSession `json:"data"`
			Cursor *struct {
				Next string `json:"next"`
			} `json:"cursor"`
		}
		query := url.Values{"limit": {"100"}}
		if cursor != "" {
			query.Set("cursor", cursor)
		}
		if err := request(ctx, "get", "/api/session?"+query.Encode(), &response); err != nil {
			return err
		}
		if response.Data == nil || response.Cursor == nil || len(response.Data) > 100 {
			return fmt.Errorf("OpenCode checkout release: malformed session/terminal inventory")
		}
		for _, session := range response.Data {
			if !strings.HasPrefix(session.ID, "ses") || seenSessions[session.ID] || !filepath.IsAbs(session.Location.Directory) {
				return fmt.Errorf("OpenCode checkout release: malformed or repeated session inventory")
			}
			seenSessions[session.ID] = true
			if !windows {
				if err := releaseSessionTerminals(ctx, request, targets, session); err != nil {
					return err
				}
			}
		}
		next := response.Cursor.Next
		if next != "" && (next == cursor || len(response.Data) == 0 || windows && seenCursors[next]) {
			return fmt.Errorf("OpenCode checkout release: session pagination did not advance")
		}
		if windows {
			if err := releaseTerminalPage(ctx, request, targets, response.Data); err != nil {
				return err
			}
		}
		if next == "" {
			return nil
		}
		seenCursors[next] = true
		cursor = next
	}
	return fmt.Errorf("OpenCode checkout release: session inventory exceeds the bounded activity check")
}

func releaseTerminalPage(ctx context.Context, request func(context.Context, string, string, any) error, targets []string, sessions []releaseSession) error {
	pageCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	var next atomic.Int32
	var workers sync.WaitGroup
	for range min(windowsTerminalReads, len(sessions)) {
		workers.Go(func() {
			for pageCtx.Err() == nil {
				i := int(next.Add(1)) - 1
				if i >= len(sessions) {
					return
				}
				if err := releaseSessionTerminals(pageCtx, request, targets, sessions[i]); err != nil {
					cancel(err)
					return
				}
			}
		})
	}
	// These reads can load a checkout. Even on refusal, do not return with
	// workers still running. Parent expiry takes precedence over sibling errors.
	workers.Wait()
	cause := context.Cause(pageCtx)
	if err := ctx.Err(); err != nil {
		return releaseContextError(err)
	}
	return cause
}

func releaseSessionTerminals(ctx context.Context, request func(context.Context, string, string, any) error, targets []string, session releaseSession) error {
	var terminals struct {
		Data []struct {
			ID        string `json:"id"`
			SessionID string `json:"sessionID"`
			Cwd       string `json:"cwd"`
			Status    string `json:"status"`
		} `json:"data"`
	}
	if err := request(ctx, "get", "/api/experimental/session/"+url.PathEscape(session.ID)+"/terminal", &terminals); err != nil {
		return err
	}
	if terminals.Data == nil || len(terminals.Data) > 1000 {
		return fmt.Errorf("OpenCode checkout release: malformed persistent-terminal inventory")
	}
	var seen map[string]bool
	if runtime.GOOS == "windows" && len(terminals.Data) > 1 {
		seen = make(map[string]bool, len(terminals.Data))
	}
	for _, terminal := range terminals.Data {
		if terminal.ID == "" || seen[terminal.ID] || terminal.SessionID != session.ID ||
			(terminal.Status != "running" && terminal.Status != "exited") || !filepath.IsAbs(terminal.Cwd) {
			return fmt.Errorf("OpenCode checkout release: unsupported persistent-terminal evidence")
		}
		if seen != nil {
			seen[terminal.ID] = true
		}
		if terminal.Status == "exited" {
			continue
		}
		for _, directory := range []string{session.Location.Directory, terminal.Cwd} {
			belongs, err := releaseBelongs(targets, directory)
			if err != nil {
				return err
			}
			if belongs {
				return fmt.Errorf("OpenCode checkout release: running persistent terminal prevents removal")
			}
		}
	}
	return nil
}
