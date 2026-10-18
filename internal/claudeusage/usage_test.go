package claudeusage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	sessionA = "aaaaaaaa-0000-4000-8000-000000000001"
	sessionB = "bbbbbbbb-0000-4000-8000-000000000002"
)

var (
	start = time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	end   = time.Date(2026, 10, 18, 13, 0, 0, 0, time.UTC)
)

// assistant is one main-conversation assistant line; ts is minutes after start.
func assistant(session, version, id string, ts, in, out, read, create int) string {
	return fmt.Sprintf(`{"type":"assistant","sessionId":%q,"version":%q,"timestamp":%q,"uuid":"u","isSidechain":false,"message":{"id":%q,"model":"m","content":[{"type":"text","text":"secret"}],"usage":{"input_tokens":%d,"output_tokens":%d,"cache_read_input_tokens":%d,"cache_creation_input_tokens":%d,"service_tier":"standard"}}}`,
		session, version, start.Add(time.Duration(ts)*time.Minute).Format(time.RFC3339Nano), id, in, out, read, create)
}

// setup records session with a transcript holding lines and returns the root.
func setup(t *testing.T, root, session string, lines ...string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), session+".jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RecordSession(root, session, path, start); err != nil {
		t.Fatal(err)
	}
}

func TestArchitectReadsEveryVerifiedVersion(t *testing.T) {
	for _, version := range []string{"2.1.289", "2.1.295", "2.1.301"} {
		root := t.TempDir()
		ts := func(m int) string { return start.Add(time.Duration(m) * time.Minute).Format(time.RFC3339Nano) }
		setup(t, root, sessionA,
			assistant(sessionA, version, "before", -1, 1000, 1000, 1000, 1000), // before activation
			`{"type":"queue-operation","sessionId":"`+sessionA+`","timestamp":"`+ts(1)+`"}`,
			`{"type":"last-prompt","sessionId":"`+sessionA+`"}`,
			assistant(sessionA, version, "m1", 2, 1, 10, 100, 1000),
			assistant(sessionA, version, "m1", 2, 1, 10, 100, 1000), // streamed repeat
			strings.Replace(assistant(sessionA, version, "side", 3, 7, 7, 7, 7), `"isSidechain":false`, `"isSidechain":true`, 1),
			assistant(sessionB, version, "other", 3, 7, 7, 7, 7), // another session's entry
			`{"type":"system","subtype":"compact_boundary","sessionId":"`+sessionA+`","version":"`+version+`","timestamp":"`+ts(4)+`"}`,
			`{"type":"user","isCompactSummary":true,"sessionId":"`+sessionA+`","version":"`+version+`","timestamp":"`+ts(4)+`","message":{"role":"user","content":"summary"}}`,
			"",
			assistant(sessionA, version, "m2", 5, 2, 20, 200, 2000),           // after compaction
			assistant(sessionA, "9.9.9", "after", 61, 1000, 1000, 1000, 1000), // after completion, unverified version ignored
		)
		got := Architect(root, []string{sessionA, sessionA}, start, end)
		want := []Capture{{Session: sessionA, Counters: Counters{Input: 3, Output: 30, CacheRead: 300, CacheCreation: 3000}}}
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("%s: Architect = %+v, want %+v", version, got, want)
		}
	}
}

func TestArchitectCountsAMessageInTwoTranscriptsOnce(t *testing.T) {
	root := t.TempDir()
	// A fork copies history: the shared message counts for the first session only.
	setup(t, root, sessionA, assistant(sessionA, "2.1.301", "shared", 1, 1, 1, 1, 1))
	setup(t, root, sessionB, assistant(sessionB, "2.1.301", "shared", 1, 1, 1, 1, 1), assistant(sessionB, "2.1.301", "own", 2, 5, 5, 5, 5))
	got := Architect(root, []string{sessionB, sessionA}, start, end)
	want := []Capture{{Session: sessionA, Counters: Counters{1, 1, 1, 1}}, {Session: sessionB, Counters: Counters{5, 5, 5, 5}}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("Architect = %+v, want %+v", got, want)
	}
}

func TestArchitectZeroIsMeasuredWhenNothingIsInWindow(t *testing.T) {
	root := t.TempDir()
	setup(t, root, sessionA, assistant(sessionA, "2.1.301", "late", 90, 1, 1, 1, 1))
	got := Architect(root, []string{sessionA}, start, end)
	if len(got) != 1 || got[0].Reason != "" || got[0].Counters != (Counters{}) {
		t.Errorf("Architect = %+v, want one measured zero", got)
	}
}

func TestArchitectReportsWhyUsageIsUnavailable(t *testing.T) {
	good := assistant(sessionA, "2.1.301", "m", 1, 1, 1, 1, 1)
	for name, tc := range map[string]struct {
		session string
		prepare func(t *testing.T, root string)
		want    string
	}{
		"not a uuid": {"../" + sessionA, func(*testing.T, string) {}, ReasonSessionNotUUID},
		"no record":  {sessionA, func(*testing.T, string) {}, ReasonPathUnrecorded},
		"missing transcript": {sessionA, func(t *testing.T, root string) {
			_ = RecordSession(root, sessionA, filepath.Join(t.TempDir(), "gone.jsonl"), start)
		}, ReasonTranscriptMissing},
		"transcript is a directory": {sessionA, func(t *testing.T, root string) { _ = RecordSession(root, sessionA, t.TempDir(), start) }, ReasonUnreadable},
		"record of another session": {sessionA, func(t *testing.T, root string) {
			setup(t, root, sessionB, good)
			data, _ := os.ReadFile(recordPath(root, sessionB))
			_ = os.WriteFile(recordPath(root, sessionA), data, 0o644)
		}, ReasonPathUnrecorded},
		"unparseable line": {sessionA, func(t *testing.T, root string) { setup(t, root, sessionA, good, `{"type":`) }, ReasonUnparseable},
		"missing counter": {sessionA, func(t *testing.T, root string) {
			setup(t, root, sessionA, strings.Replace(good, `"output_tokens":1,`, "", 1))
		}, ReasonUnparseable},
		"missing message id": {sessionA, func(t *testing.T, root string) { setup(t, root, sessionA, strings.Replace(good, `"id":"m",`, "", 1)) }, ReasonUnparseable},
		"bad timestamp": {sessionA, func(t *testing.T, root string) {
			setup(t, root, sessionA, strings.Replace(good, "2026-10-18T", "2026/10/18 ", 1))
		}, ReasonUnparseable},
		"unverified version": {sessionA, func(t *testing.T, root string) {
			setup(t, root, sessionA, good, assistant(sessionA, "2.1.300", "n", 2, 1, 1, 1, 1))
		}, ReasonUnverifiedVersion},
		"missing version": {sessionA, func(t *testing.T, root string) {
			setup(t, root, sessionA, strings.Replace(good, `"version":"2.1.301",`, "", 1))
		}, ReasonUnverifiedVersion},
		"unverified user line": {sessionA, func(t *testing.T, root string) {
			setup(t, root, sessionA, good, `{"type":"user","sessionId":"`+sessionA+`","version":"3.0.0","timestamp":"`+start.Format(time.RFC3339)+`"}`)
		}, ReasonUnverifiedVersion},
	} {
		root := t.TempDir()
		tc.prepare(t, root)
		got := Architect(root, []string{tc.session}, start, end)
		if len(got) != 1 || got[0].Session != tc.session || got[0].Reason != tc.want || got[0].Counters != (Counters{}) {
			t.Errorf("%s: Architect = %+v, want reason %s and no counters", name, got, tc.want)
		}
	}
	if got := Architect(t.TempDir(), nil, start, end); len(got) != 1 || got[0] != (Capture{Reason: ReasonNoSession}) {
		t.Errorf("no sessions: Architect = %+v", got)
	}
}

func TestArchitectUnavailableSessionClaimsNoMessages(t *testing.T) {
	root := t.TempDir()
	setup(t, root, sessionA, assistant(sessionA, "2.1.301", "shared", 1, 1, 1, 1, 1), `{"type":`)
	setup(t, root, sessionB, assistant(sessionB, "2.1.301", "shared", 1, 4, 4, 4, 4))
	got := Architect(root, []string{sessionA, sessionB}, start, end)
	if len(got) != 2 || got[0].Reason != ReasonUnparseable || got[1].Reason != "" || got[1].Counters != (Counters{4, 4, 4, 4}) {
		t.Errorf("Architect = %+v", got)
	}
}

func TestRecordSessionValidatesIdentity(t *testing.T) {
	root := t.TempDir()
	abs := filepath.Join(root, "t.jsonl")
	for _, bad := range [][2]string{{"../x", abs}, {"not-a-uuid", abs}, {sessionA, "relative.jsonl"}} {
		if err := RecordSession(root, bad[0], bad[1], start); err == nil {
			t.Errorf("RecordSession(%q, %q) = nil, want error", bad[0], bad[1])
		}
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(SessionsDir))); !os.IsNotExist(err) {
		t.Errorf("rejected records created storage (stat err %v)", err)
	}
	if err := RecordSession(root, sessionA, abs, start); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(recordPath(root, sessionA))
	if err != nil {
		t.Fatal(err)
	}
	var rec map[string]string
	if err := json.Unmarshal(data, &rec); err != nil || len(rec) != 3 || rec["session_id"] != sessionA || rec["transcript_path"] != abs {
		t.Errorf("record = %s (%v), want only the session id, transcript path and time", data, err)
	}
}

func TestRecordSessionConcurrentWritersAndReaderKeepRecordsWhole(t *testing.T) {
	root := t.TempDir()
	abs := filepath.Join(root, "t.jsonl")
	if err := RecordSession(root, sessionA, abs, start); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	sessions := make([]string, 16)
	for i := range sessions {
		sessions[i] = fmt.Sprintf("cccccccc-0000-4000-8000-%012d", i)
		wg.Add(1)
		go func(s string) {
			defer wg.Done()
			if err := RecordSession(root, s, abs, start); err != nil {
				t.Errorf("RecordSession(%s): %v", s, err)
			}
		}(sessions[i])
	}
	// The same session restarting while a reader reads its record: the reader
	// always sees a whole record, and the writer's replacement lands.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 200 {
			if err := RecordSession(root, sessionA, abs, start); err != nil {
				t.Errorf("RecordSession during reads: %v", err)
				return
			}
		}
	}()
	for range 200 {
		if got := transcriptPath(root, sessionA); got != abs {
			t.Errorf("transcriptPath during rewrite = %q, want %q", got, abs)
			break
		}
	}
	<-done
	wg.Wait()
	for _, s := range sessions {
		if got := transcriptPath(root, s); got != abs {
			t.Errorf("transcriptPath(%s) = %q, want %q", s, got, abs)
		}
	}
}
