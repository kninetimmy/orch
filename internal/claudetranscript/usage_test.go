package claudetranscript

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestContextTokensReadsLatestMainEntryOfVersion2_1_295(t *testing.T) {
	got, ok := ContextTokens(filepath.Join("testdata", "usage_2.1.295.jsonl"))
	// 2 input + 24362 cache creation + 32959 cache read; output and the later
	// sidechain entry are excluded.
	if !ok || got != 57323 {
		t.Fatalf("ContextTokens = %d, %v; want 57323, true", got, ok)
	}
}

func TestContextTokensYieldsNothingOnUnrecognizedInput(t *testing.T) {
	good := `{"type":"assistant","isSidechain":false,"version":"2.1.295","message":{"usage":{"input_tokens":1,"cache_creation_input_tokens":2,"cache_read_input_tokens":3}}}`
	for name, content := range map[string]string{
		"empty":               "",
		"no assistant entry":  `{"type":"user","version":"2.1.295"}`,
		"unknown version":     strings.Replace(good, "2.1.295", "9.9.9", 1),
		"missing version":     strings.Replace(good, `"version":"2.1.295",`, "", 1),
		"missing cache field": strings.Replace(good, `"cache_read_input_tokens":3`, `"other":3`, 1),
		"newer unknown entry": good + "\n" + strings.Replace(good, "2.1.295", "2.2.0", 1),
		"not json":            "garbage",
		"only sidechain":      strings.Replace(good, `"isSidechain":false`, `"isSidechain":true`, 1),
	} {
		path := filepath.Join(t.TempDir(), "t.jsonl")
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if got, ok := ContextTokens(path); ok {
			t.Errorf("%s: ContextTokens = %d, true; want not ok", name, got)
		}
	}
	if _, ok := ContextTokens(filepath.Join(t.TempDir(), "missing.jsonl")); ok {
		t.Error("missing file reported ok")
	}
}

func TestContextTokensReadsOnlyTheTail(t *testing.T) {
	line := `{"type":"assistant","isSidechain":false,"version":"2.1.295","message":{"usage":{"input_tokens":1,"cache_creation_input_tokens":2,"cache_read_input_tokens":3}}}` + "\n"
	pad := `{"type":"user","note":"` + strings.Repeat("x", 1<<20) + `"}` + "\n"
	path := filepath.Join(t.TempDir(), "t.jsonl")
	// The only assistant entry lies beyond the tail window: unknown, not guessed.
	if err := os.WriteFile(path, []byte(line+pad+pad+pad), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, ok := ContextTokens(path); ok {
		t.Errorf("ContextTokens = %d, true; want not ok", got)
	}
	if err := os.WriteFile(path, []byte(pad+pad+pad+line), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, ok := ContextTokens(path); !ok || got != 6 {
		t.Errorf("ContextTokens = %d, %v; want 6, true", got, ok)
	}
}
