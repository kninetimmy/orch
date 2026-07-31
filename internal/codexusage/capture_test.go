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
	got, ok := TotalTokens(fixture(t, "exact"), parentThread, executorTask)
	if !ok {
		t.Fatal("TotalTokens reported unavailable")
	}
	if got != 782763 {
		t.Errorf("total_tokens = %d, want 782763", got)
	}
}

func TestTotalTokensUnavailable(t *testing.T) {
	t.Run("missing persistence", func(t *testing.T) {
		if _, ok := TotalTokens(filepath.Join(t.TempDir(), "missing"), parentThread, executorTask); ok {
			t.Error("TotalTokens reported usage for missing persistence")
		}
	})

	t.Run("no matching child", func(t *testing.T) {
		if _, ok := TotalTokens(fixture(t, "exact"), parentThread, "/root/missing"); ok {
			t.Error("TotalTokens reported usage for no matching child")
		}
	})

	t.Run("parent rollout", func(t *testing.T) {
		if _, ok := TotalTokens(fixture(t, "exact"), parentThread, "/root"); ok {
			t.Error("TotalTokens reported the parent session's usage")
		}
	})

	t.Run("malformed final total", func(t *testing.T) {
		if _, ok := TotalTokens(fixture(t, "unavailable"), parentThread, executorTask); ok {
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
		if _, ok := TotalTokens(dir, parentThread, executorTask); ok {
			t.Error("TotalTokens reported usage for ambiguous children")
		}
	})
}
