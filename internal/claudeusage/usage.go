// Package claudeusage counts a Claude Code Architect session's own token
// usage from its transcript. The SessionStart hook records each session's
// transcript path; orch run complete reads the transcripts of the sessions
// that ran the run's verbs. The transcript format is internal to Claude Code,
// so only the versions in verifiedVersions are read; anything else is
// reported unavailable rather than guessed. Nothing from a transcript but
// timestamps and token counters ever leaves this package.
package claudeusage

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"time"
)

// verifiedVersions are the Claude Code versions whose summed main-session
// usage was checked against Claude Code's own per-session totals. Add one only
// after the same check on a real, subagent-free transcript of that version,
// and add it to TestArchitectReadsEveryVerifiedVersion.
var verifiedVersions = []string{"2.1.289", "2.1.295", "2.1.301"}

// SessionsDir is where the SessionStart hook records sessions, inside the
// git-ignored metrics directory. One file per session, named by its UUID.
const SessionsDir = ".orchestrator/metrics/claude-sessions"

// The unavailable reasons a Capture can carry.
const (
	ReasonNoSession         = "no-session-recorded"
	ReasonSessionNotUUID    = "session-id-not-uuid"
	ReasonPathUnrecorded    = "transcript-path-unrecorded"
	ReasonTranscriptMissing = "transcript-missing"
	ReasonUnreadable        = "transcript-unreadable"
	ReasonUnparseable       = "transcript-unparseable"
	ReasonUnverifiedVersion = "unverified-claude-code-version"
)

var uuidPattern = regexp.MustCompile(`^(?i)[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// ValidSession reports whether id is a UUID. Only such an id may name, find or
// read a file.
func ValidSession(id string) bool { return uuidPattern.MatchString(id) }

// sessionRecord is everything recorded about a session.
type sessionRecord struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	RecordedAt     string `json:"recorded_at"`
}

func recordPath(root, sessionID string) string {
	return filepath.Join(root, filepath.FromSlash(SessionsDir), sessionID+".json")
}

// RecordSession records sessionID's transcript path under root, replacing an
// earlier record of the same session atomically, so a concurrent reader sees
// the old or the new record whole. Sessions never share a file, so sessions
// starting together cannot overwrite each other. It takes no lock.
// shortcut: records are never pruned (one small file per session); add
// pruning if the directory's size ever matters.
func RecordSession(root, sessionID, transcriptPath string, now time.Time) error {
	if !ValidSession(sessionID) {
		return fmt.Errorf("session id %q is not a UUID", sessionID)
	}
	if !filepath.IsAbs(transcriptPath) {
		return fmt.Errorf("transcript path %q is not absolute", transcriptPath)
	}
	data, err := json.Marshal(sessionRecord{SessionID: sessionID, TranscriptPath: transcriptPath, RecordedAt: now.UTC().Format(time.RFC3339Nano)})
	if err != nil {
		return err
	}
	dir := filepath.Join(root, filepath.FromSlash(SessionsDir))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, "session-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = retry(func() error { return os.Rename(tmp, recordPath(root, sessionID)) })
	}
	if err != nil {
		_ = os.Remove(tmp) // best effort; any earlier record is untouched
	}
	return err
}

// transcriptPath returns the path recorded for sessionID, or "" when none was
// recorded for that same session.
func transcriptPath(root, sessionID string) string {
	var data []byte
	err := retry(func() (err error) { data, err = os.ReadFile(recordPath(root, sessionID)); return err })
	if err != nil {
		return ""
	}
	var rec sessionRecord
	if json.Unmarshal(data, &rec) != nil || rec.SessionID != sessionID || !filepath.IsAbs(rec.TranscriptPath) {
		return ""
	}
	return rec.TranscriptPath
}

// retry runs fn up to 20 times, 10ms apart, while it fails with anything but
// not-exist: Windows refuses to replace a file another process has open, and to
// open one being replaced, for that instant. It waits on no lock.
func retry(fn func() error) error {
	err := fn()
	for range 19 {
		if err == nil || errors.Is(err, fs.ErrNotExist) {
			break
		}
		time.Sleep(10 * time.Millisecond)
		err = fn()
	}
	return err
}

// Counters are a session's four counted token totals.
type Counters struct {
	Input, Output, CacheRead, CacheCreation int64
}

// Capture is one session's result: Counters when Reason is empty, otherwise
// nothing but the reason.
type Capture struct {
	Session  string
	Counters Counters
	Reason   string
}

// Architect counts, for each session in sessions, the usage of its own
// main-conversation assistant messages timestamped within [start, end]. Each
// message id is counted once across all of the sessions; sessions are taken in
// sorted order, so a message present in two transcripts counts for the first.
// A session whose usage cannot be established carries a reason instead and
// claims no message ids. No sessions at all yields one ReasonNoSession capture.
func Architect(root string, sessions []string, start, end time.Time) []Capture {
	if len(sessions) == 0 {
		return []Capture{{Reason: ReasonNoSession}}
	}
	sorted := slices.Clone(sessions)
	sort.Strings(sorted)
	seen := map[string]bool{}
	var out []Capture
	for _, s := range slices.Compact(sorted) {
		c := Capture{Session: s, Reason: ReasonSessionNotUUID}
		if ValidSession(s) {
			c.Reason = ReasonPathUnrecorded
			if path := transcriptPath(root, s); path != "" {
				var msgs []message
				msgs, c.Reason = read(path, s, start, end)
				for _, m := range msgs {
					if seen[m.id] {
						continue
					}
					seen[m.id] = true
					c.Counters.Input += m.counters.Input
					c.Counters.Output += m.counters.Output
					c.Counters.CacheRead += m.counters.CacheRead
					c.Counters.CacheCreation += m.counters.CacheCreation
				}
			}
		}
		out = append(out, c)
	}
	return out
}

// entry is the part of one transcript line this reader looks at; message
// content is never decoded into it.
type entry struct {
	Type        string `json:"type"`
	SessionID   string `json:"sessionId"`
	Version     string `json:"version"`
	Timestamp   string `json:"timestamp"`
	IsSidechain bool   `json:"isSidechain"`
	Message     struct {
		ID    string `json:"id"`
		Usage *struct {
			Input         *int64 `json:"input_tokens"`
			Output        *int64 `json:"output_tokens"`
			CacheRead     *int64 `json:"cache_read_input_tokens"`
			CacheCreation *int64 `json:"cache_creation_input_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

type message struct {
	id       string
	counters Counters
}

// read returns session's in-window main-conversation assistant messages, first
// occurrence of each message id only, or a reason when the whole transcript
// cannot be read and parsed or an in-window entry of the session is from an
// unverified version. Entries of other sessions, sidechain entries and entries
// outside the window are skipped.
func read(path, session string, start, end time.Time) ([]message, string) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ReasonTranscriptMissing
	}
	if err != nil {
		return nil, ReasonUnreadable
	}
	defer func() { _ = f.Close() }()
	r := bufio.NewReader(f)
	seen := map[string]bool{}
	var msgs []message
	for {
		line, err := r.ReadBytes('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, ReasonUnreadable
		}
		if line = bytes.TrimSpace(line); len(line) > 0 {
			var e entry
			if json.Unmarshal(line, &e) != nil {
				return nil, ReasonUnparseable
			}
			m, counted, reason := classify(e, session, start, end)
			if reason != "" {
				return nil, reason
			}
			if counted && !seen[m.id] {
				seen[m.id] = true
				msgs = append(msgs, m)
			}
		}
		if errors.Is(err, io.EOF) {
			return msgs, ""
		}
	}
}

// classify reports whether e is a counted message of session within the window,
// or the reason the transcript cannot be used.
func classify(e entry, session string, start, end time.Time) (message, bool, string) {
	if e.SessionID != session || e.IsSidechain {
		return message{}, false, ""
	}
	if e.Timestamp == "" {
		if e.Type == "assistant" {
			return message{}, false, ReasonUnparseable
		}
		return message{}, false, "" // untimed bookkeeping entries are outside any window
	}
	at, err := time.Parse(time.RFC3339Nano, e.Timestamp)
	if err != nil {
		return message{}, false, ReasonUnparseable
	}
	if at.Before(start) || at.After(end) {
		return message{}, false, ""
	}
	if (e.Version != "" || e.Type == "assistant") && !slices.Contains(verifiedVersions, e.Version) {
		return message{}, false, ReasonUnverifiedVersion
	}
	if e.Type != "assistant" {
		return message{}, false, ""
	}
	u := e.Message.Usage
	if e.Message.ID == "" || u == nil || u.Input == nil || u.Output == nil || u.CacheRead == nil || u.CacheCreation == nil ||
		*u.Input < 0 || *u.Output < 0 || *u.CacheRead < 0 || *u.CacheCreation < 0 {
		return message{}, false, ReasonUnparseable
	}
	return message{id: e.Message.ID, counters: Counters{*u.Input, *u.Output, *u.CacheRead, *u.CacheCreation}}, true, ""
}
