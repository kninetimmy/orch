package codexusage

import (
	"os"
	"path/filepath"
	"testing"
)

const (
	parentThread = "parent-139"
	executorTask = "/root/issue_139_executor"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join("testdata", name)
}

func TestTotalTokensSelectsOnlyExactCompletedChild(t *testing.T) {
	got, ok := TotalTokens(fixture(t, "exact"), parentThread, executorTask, nil)
	if !ok {
		t.Fatal("TotalTokens reported unavailable")
	}
	if got != 782763 {
		t.Errorf("total_tokens = %d, want 782763", got)
	}
}

func TestTotalTokensIsolatesOnlyIdentifiedNonMatchingCorruption(t *testing.T) {
	exact, err := os.ReadFile(filepath.Join(fixture(t, "exact"), "executor.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	unrelated, err := os.ReadFile(filepath.Join(fixture(t, "exact"), "sibling.jsonl"))
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name  string
		other []byte
		ok    bool
	}{
		{
			name:  "identified unrelated rollout",
			other: append(unrelated, []byte("this is not json\n")...),
			ok:    true,
		},
		{
			name:  "unidentified rollout",
			other: []byte("{\"type\":\"session_meta\",\"payload\":{}}\nthis is not json\n"),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "exact.jsonl"), exact, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "other.jsonl"), tc.other, 0o644); err != nil {
				t.Fatal(err)
			}

			got, ok := TotalTokens(dir, parentThread, executorTask, nil)
			if ok != tc.ok {
				t.Fatalf("TotalTokens availability = %t, want %t", ok, tc.ok)
			}
			if ok && got != 782763 {
				t.Errorf("total_tokens = %d, want 782763", got)
			}
		})
	}
}

func TestTotalTokensReturnsExactResumeDelta(t *testing.T) {
	for _, tc := range []struct {
		name     string
		previous int64
		want     int64
	}{
		{name: "additional usage", previous: 782000, want: 763},
		{name: "zero usage", previous: 782763, want: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := TotalTokens(fixture(t, "exact"), parentThread, executorTask, &tc.previous)
			if !ok {
				t.Fatal("TotalTokens reported unavailable")
			}
			if got != tc.want {
				t.Errorf("total_tokens = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestTotalTokensUnavailable(t *testing.T) {
	t.Run("missing persistence", func(t *testing.T) {
		if _, ok := TotalTokens(filepath.Join(t.TempDir(), "missing"), parentThread, executorTask, nil); ok {
			t.Error("TotalTokens reported usage for missing persistence")
		}
	})

	t.Run("no matching child", func(t *testing.T) {
		if _, ok := TotalTokens(fixture(t, "exact"), parentThread, "/root/missing", nil); ok {
			t.Error("TotalTokens reported usage for no matching child")
		}
	})

	t.Run("parent rollout", func(t *testing.T) {
		if _, ok := TotalTokens(fixture(t, "exact"), parentThread, "/root", nil); ok {
			t.Error("TotalTokens reported the parent session's usage")
		}
	})

	t.Run("malformed final total", func(t *testing.T) {
		if _, ok := TotalTokens(fixture(t, "unavailable"), parentThread, executorTask, nil); ok {
			t.Error("TotalTokens reported usage for malformed final total")
		}
	})

	t.Run("ambiguous children", func(t *testing.T) {
		dir := t.TempDir()
		data, err := os.ReadFile(filepath.Join(fixture(t, "exact"), "executor.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"one.jsonl", "two.jsonl"} {
			if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if _, ok := TotalTokens(dir, parentThread, executorTask, nil); ok {
			t.Error("TotalTokens reported usage for ambiguous children")
		}
	})

	t.Run("invalid previous total", func(t *testing.T) {
		for _, previous := range []int64{-1, 782764} {
			if _, ok := TotalTokens(fixture(t, "exact"), parentThread, executorTask, &previous); ok {
				t.Errorf("TotalTokens reported usage for previous_total_tokens = %d", previous)
			}
		}
	})
}
