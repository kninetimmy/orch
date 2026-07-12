package guard

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// codexPayload marshals a minimal Codex CLI PreToolUse event whose
// tool_input carries the given apply_patch envelope as its command.
func codexPayload(t *testing.T, toolName, envelope, cwd string) []byte {
	t.Helper()
	m := map[string]any{
		"tool_name":  toolName,
		"tool_input": map[string]string{"command": envelope},
	}
	if cwd != "" {
		m["cwd"] = cwd
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestCodexTools(t *testing.T) {
	got := CodexTools()
	want := []string{"apply_patch"}
	if len(got) != len(want) {
		t.Fatalf("CodexTools() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("CodexTools() = %v, want %v", got, want)
		}
	}
}

const addUpdateDeleteEnvelope = `*** Begin Patch
*** Add File: new.txt
+contents
*** Update File: existing.txt
@@ hunk header
-old
+new
*** Delete File: gone.txt
*** End Patch`

func TestPathsFromCodexEventAddUpdateDelete(t *testing.T) {
	root := t.TempDir()
	got, err := PathsFromCodexEvent(codexPayload(t, "apply_patch", addUpdateDeleteEnvelope, root))
	if err != nil {
		t.Fatalf("PathsFromCodexEvent err = %v", err)
	}
	want := []string{
		filepath.Join(root, "new.txt"),
		filepath.Join(root, "existing.txt"),
		filepath.Join(root, "gone.txt"),
	}
	if len(got) != len(want) {
		t.Fatalf("paths = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("paths[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

const moveEnvelope = `*** Begin Patch
*** Update File: old/name.txt
@@ hunk header
-old
+new
*** Move to: new/name.txt
*** End Patch`

func TestPathsFromCodexEventMoveExtractsBothPaths(t *testing.T) {
	root := t.TempDir()
	got, err := PathsFromCodexEvent(codexPayload(t, "apply_patch", moveEnvelope, root))
	if err != nil {
		t.Fatalf("PathsFromCodexEvent err = %v", err)
	}
	want := []string{
		filepath.Join(root, "old", "name.txt"),
		filepath.Join(root, "new", "name.txt"),
	}
	if len(got) != len(want) {
		t.Fatalf("paths = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("paths[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestPathsFromCodexEventRelativeResolvedAgainstCWD(t *testing.T) {
	root := t.TempDir()
	envelope := "*** Begin Patch\n*** Add File: sub/new.txt\n+x\n*** End Patch"
	got, err := PathsFromCodexEvent(codexPayload(t, "apply_patch", envelope, root))
	if err != nil {
		t.Fatalf("PathsFromCodexEvent err = %v", err)
	}
	want := filepath.Join(root, "sub", "new.txt")
	if len(got) != 1 || got[0] != want {
		t.Fatalf("paths = %v, want [%q]", got, want)
	}
}

func TestPathsFromCodexEventRelativeNoCWDErrors(t *testing.T) {
	envelope := "*** Begin Patch\n*** Add File: sub/new.txt\n+x\n*** End Patch"
	_, err := PathsFromCodexEvent(codexPayload(t, "apply_patch", envelope, ""))
	if err == nil {
		t.Fatal("PathsFromCodexEvent err = nil, want relative-path-no-cwd error")
	}
}

func TestPathsFromCodexEventAbsolutePathsPassThrough(t *testing.T) {
	root := t.TempDir()
	abs := filepath.Join(root, "already", "absolute.txt")
	envelope := "*** Begin Patch\n*** Add File: " + abs + "\n+x\n*** End Patch"
	got, err := PathsFromCodexEvent(codexPayload(t, "apply_patch", envelope, ""))
	if err != nil {
		t.Fatalf("PathsFromCodexEvent err = %v", err)
	}
	if len(got) != 1 || got[0] != abs {
		t.Fatalf("paths = %v, want [%q]", got, abs)
	}
}

func TestPathsFromCodexEventBashErrorsUnknownTool(t *testing.T) {
	payload, err := json.Marshal(map[string]any{
		"tool_name":  "Bash",
		"tool_input": map[string]string{"command": "rm -rf /"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = PathsFromCodexEvent(payload)
	if !errors.Is(err, ErrUnknownTool) {
		t.Fatalf("err = %v, want ErrUnknownTool", err)
	}
}

func TestPathsFromCodexEventMCPToolErrorsUnknownTool(t *testing.T) {
	payload, err := json.Marshal(map[string]any{
		"tool_name":  "mcp__memhub__recall",
		"tool_input": map[string]string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = PathsFromCodexEvent(payload)
	if !errors.Is(err, ErrUnknownTool) {
		t.Fatalf("err = %v, want ErrUnknownTool", err)
	}
}

func TestPathsFromCodexEventMalformedJSON(t *testing.T) {
	_, err := PathsFromCodexEvent([]byte(`{ not json`))
	if err == nil {
		t.Fatal("PathsFromCodexEvent err = nil, want parse error")
	}
	if errors.Is(err, ErrUnknownTool) || errors.Is(err, ErrPatchEnvelope) {
		t.Fatalf("err = %v, want a plain decode error, not a deny sentinel", err)
	}
}

func TestPathsFromCodexEventMissingBeginPatch(t *testing.T) {
	envelope := "*** Add File: new.txt\n+x\n*** End Patch"
	_, err := PathsFromCodexEvent(codexPayload(t, "apply_patch", envelope, "/repo"))
	if !errors.Is(err, ErrPatchEnvelope) {
		t.Fatalf("err = %v, want ErrPatchEnvelope", err)
	}
}

func TestPathsFromCodexEventMissingEndPatch(t *testing.T) {
	envelope := "*** Begin Patch\n*** Add File: new.txt\n+x"
	_, err := PathsFromCodexEvent(codexPayload(t, "apply_patch", envelope, "/repo"))
	if !errors.Is(err, ErrPatchEnvelope) {
		t.Fatalf("err = %v, want ErrPatchEnvelope", err)
	}
}

func TestPathsFromCodexEventUnrecognizedDirective(t *testing.T) {
	envelope := "*** Begin Patch\n*** Frobnicate: new.txt\n*** End Patch"
	_, err := PathsFromCodexEvent(codexPayload(t, "apply_patch", envelope, "/repo"))
	if !errors.Is(err, ErrPatchEnvelope) {
		t.Fatalf("err = %v, want ErrPatchEnvelope", err)
	}
}

func TestPathsFromCodexEventZeroPaths(t *testing.T) {
	envelope := "*** Begin Patch\n*** End Patch"
	_, err := PathsFromCodexEvent(codexPayload(t, "apply_patch", envelope, "/repo"))
	if !errors.Is(err, ErrPatchEnvelope) {
		t.Fatalf("err = %v, want ErrPatchEnvelope", err)
	}
}

func TestPathsFromCodexEventEmptyPathAfterDirective(t *testing.T) {
	envelope := "*** Begin Patch\n*** Add File: \n*** End Patch"
	_, err := PathsFromCodexEvent(codexPayload(t, "apply_patch", envelope, "/repo"))
	if !errors.Is(err, ErrPatchEnvelope) {
		t.Fatalf("err = %v, want ErrPatchEnvelope", err)
	}
}

func TestPathsFromCodexEventCRLFMatchesLF(t *testing.T) {
	root := t.TempDir()
	crlf := strings.ReplaceAll(addUpdateDeleteEnvelope, "\n", "\r\n")

	lfPaths, err := PathsFromCodexEvent(codexPayload(t, "apply_patch", addUpdateDeleteEnvelope, root))
	if err != nil {
		t.Fatalf("LF envelope err = %v", err)
	}
	crlfPaths, err := PathsFromCodexEvent(codexPayload(t, "apply_patch", crlf, root))
	if err != nil {
		t.Fatalf("CRLF envelope err = %v", err)
	}
	if len(lfPaths) != len(crlfPaths) {
		t.Fatalf("CRLF paths = %v, want %v", crlfPaths, lfPaths)
	}
	for i := range lfPaths {
		if lfPaths[i] != crlfPaths[i] {
			t.Errorf("CRLF paths[%d] = %q, want %q", i, crlfPaths[i], lfPaths[i])
		}
	}
}
