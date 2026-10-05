package codexnative

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// This entire replay seam is compiled only into tests. Responses are authored
// locally; their usage, model and assistant text are never inference evidence.
type replayTool struct {
	Name      string
	Namespace string
	Kind      string
}

type replayCall struct {
	ID   string
	Tool replayTool
	Args string
	Want string
}

type toolReplay struct {
	mu       sync.Mutex
	tools    []replayTool
	calls    []replayCall
	outputs  map[string]json.RawMessage
	requests int
	err      error
	plan     func([]replayTool) ([]replayCall, error)
	advance  func(map[string]json.RawMessage) ([]replayCall, error)
	stages   int
	canary   *replayMCP
}

func advertisedReplayTools(raw json.RawMessage) ([]replayTool, error) {
	var specs []struct {
		Type  string          `json:"type"`
		Name  string          `json:"name"`
		Tools json.RawMessage `json:"tools"`
	}
	if json.Unmarshal(raw, &specs) != nil || len(specs) == 0 || len(specs) > 128 {
		return nil, errors.New("replay limitation: advertised tools missing or malformed")
	}
	var tools []replayTool
	for _, spec := range specs {
		if spec.Type == "namespace" {
			nested, err := advertisedReplayTools(spec.Tools)
			if err != nil || spec.Name == "" {
				return nil, errors.New("replay limitation: invalid tool namespace")
			}
			for _, tool := range nested {
				if tool.Namespace != "" {
					return nil, errors.New("replay limitation: nested tool namespace")
				}
				tool.Namespace = spec.Name
				tools = append(tools, tool)
			}
			continue
		}
		if spec.Type == "tool_search" {
			tools = append(tools, replayTool{Name: "tool_search", Kind: "tool_search"})
			continue
		}
		if (spec.Type != "function" && spec.Type != "custom") || spec.Name == "" {
			return nil, errors.New("replay limitation: unexpected hosted/deferred tool or invalid tool name")
		}
		tools = append(tools, replayTool{Name: spec.Name, Kind: spec.Type})
	}
	slices.SortFunc(tools, func(a, b replayTool) int { return strings.Compare(a.Namespace+"."+a.Name, b.Namespace+"."+b.Name) })
	for i := 1; i < len(tools); i++ {
		if tools[i].Namespace == tools[i-1].Namespace && tools[i].Name == tools[i-1].Name {
			return nil, errors.New("replay limitation: duplicate advertised tool")
		}
	}
	return tools, nil
}

func (r *toolReplay) ServeHTTP(w http.ResponseWriter, request *http.Request) {
	if request.URL.Path == "/mcp" && r.canary != nil {
		r.canary.ServeHTTP(w, request)
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if request.Method != "POST" || !strings.HasSuffix(request.URL.Path, "/responses") || request.Header.Get("Authorization") != "Bearer orch-synthetic-replay" {
		r.err = errors.New("replay limitation: unexpected endpoint or nonsynthetic authentication")
		http.Error(w, "unexpected replay request", http.StatusBadRequest)
		return
	}
	data, err := io.ReadAll(io.LimitReader(request.Body, maxMessageBytes+1))
	var payload struct {
		Tools json.RawMessage `json:"tools"`
		Input []struct {
			Type   string          `json:"type"`
			CallID string          `json:"call_id"`
			Output json.RawMessage `json:"output"`
			Tools  json.RawMessage `json:"tools"`
			Status string          `json:"status"`
		} `json:"input"`
	}
	if err != nil || len(data) > maxMessageBytes || json.Unmarshal(data, &payload) != nil {
		r.err = errors.New("replay limitation: malformed or oversized native request")
		http.Error(w, "invalid replay request", http.StatusBadRequest)
		return
	}
	tools, err := advertisedReplayTools(payload.Tools)
	if err != nil {
		r.err = err
		http.Error(w, "invalid tool inventory", http.StatusBadRequest)
		return
	}
	if r.requests == 0 {
		r.tools, r.outputs = tools, map[string]json.RawMessage{}
		r.calls, err = r.plan(tools)
		if err != nil {
			r.err = err
			http.Error(w, "unsupported tool boundary", http.StatusBadRequest)
			return
		}
	} else if !slices.Equal(tools, r.tools) {
		r.err = errors.New("replay limitation: advertised tool set changed during replay")
		http.Error(w, "changed tool inventory", http.StatusBadRequest)
		return
	}
	for _, item := range payload.Input {
		if item.Type == "tool_search_output" {
			if item.CallID == "" || item.Status != "completed" || len(item.Tools) == 0 {
				r.err = errors.New("replay limitation: malformed native discovery output")
				http.Error(w, "invalid discovery output", 400)
				return
			}
			r.outputs[item.CallID] = append(json.RawMessage(nil), item.Tools...)
		}
		if item.Type == "function_call_output" || item.Type == "custom_tool_call_output" {
			if item.CallID == "" || len(item.Output) == 0 || bytes.Equal(item.Output, []byte("null")) {
				r.err = errors.New("replay limitation: malformed native tool output")
				http.Error(w, "invalid tool output", http.StatusBadRequest)
				return
			}
			r.outputs[item.CallID] = append(json.RawMessage(nil), item.Output...)
		}
	}
	var next []replayCall
	if r.requests == 0 {
		next = r.calls
	} else if r.advance != nil {
		next, err = r.advance(r.outputs)
		if err != nil {
			r.err = err
			http.Error(w, "missing native process evidence", http.StatusBadRequest)
			return
		}
		r.calls = append(r.calls, next...)
	}
	if len(next) != 0 {
		r.stages++
	}
	r.requests++
	w.Header().Set("Content-Type", "text/event-stream")
	send := func(event map[string]any) {
		data, encodeErr := json.Marshal(event)
		if encodeErr != nil {
			r.err = errors.New("replay limitation: reply encoding failed")
			return
		}
		if _, writeErr := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event["type"], data); writeErr != nil {
			r.err = errors.New("replay limitation: reply delivery failed")
		}
	}
	id := fmt.Sprintf("orch-replay-%d", r.requests)
	send(map[string]any{"type": "response.created", "response": map[string]any{"id": id}})
	if len(next) != 0 {
		for _, call := range next {
			item := map[string]any{"type": "function_call", "call_id": call.ID, "name": call.Tool.Name, "arguments": call.Args}
			if call.Tool.Namespace != "" {
				item["namespace"] = call.Tool.Namespace
			}
			if call.Tool.Kind == "custom" {
				item["type"], item["input"] = "custom_tool_call", call.Args
				delete(item, "arguments")
			}
			if call.Tool.Kind == "tool_search" {
				var arguments any
				if json.Unmarshal([]byte(call.Args), &arguments) != nil {
					r.err = errors.New("replay limitation: malformed discovery call")
					return
				}
				item = map[string]any{"type": "tool_search_call", "call_id": call.ID, "execution": "client", "arguments": arguments}
			}
			send(map[string]any{"type": "response.output_item.done", "item": item})
		}
	} else {
		send(map[string]any{"type": "response.output_item.done", "item": map[string]any{"type": "message", "role": "assistant", "id": "orch-replay-final", "content": []any{map[string]any{"type": "output_text", "text": "Synthetic replay complete."}}}})
	}
	send(map[string]any{"type": "response.completed", "response": map[string]any{"id": id, "usage": map[string]any{"input_tokens": 0, "output_tokens": 0, "total_tokens": 0}}})
}

func (r *toolReplay) validate(cleanup error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if cleanup != nil {
		return fmt.Errorf("replay limitation: native cleanup failed: %w", cleanup)
	}
	if r.err != nil {
		return r.err
	}
	if r.requests != r.stages+1 || r.stages == 0 || len(r.calls) == 0 || len(r.outputs) != len(r.calls) {
		return errors.New("replay limitation: missing native tool execution or completion")
	}
	for _, call := range r.calls {
		output, ok := r.outputs[call.ID]
		if !ok {
			return errors.New("replay limitation: missing dispatched call output")
		}
		if err := validateReplayOutput(call, output); err != nil {
			return err
		}
	}
	return nil
}

func validateReplayOutput(call replayCall, output json.RawMessage) error {
	var text string
	if json.Unmarshal(output, &text) != nil {
		var contents []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(output, &contents) == nil {
			for _, content := range contents {
				if content.Type == "input_text" {
					text += content.Text + "\n"
				}
			}
		}
	}
	lower := strings.ToLower(text)
	denied := strings.Contains(lower, "permission denied") || strings.Contains(lower, "access is denied") || strings.Contains(lower, "access denied")
	passed := false
	switch call.Want {
	case "":
		passed = true // fixtures for the evidence validator itself
	case "patch":
		passed = strings.Contains(text, "Success. Updated the following files")
	case "patch-denied":
		passed = denied || strings.Contains(lower, "patch rejected: writing outside of the project")
	case "patch-read-denied":
		passed = denied && strings.Contains(lower, "read")
	case "image":
		var contents []struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(output, &contents) == nil {
			for _, content := range contents {
				if content.Type == "input_image" {
					passed = true
				}
			}
		}
	case "image-denied":
		passed = denied && (strings.Contains(lower, "locate image") || strings.Contains(lower, "read image"))
	case "unavailable":
		passed = strings.Contains(lower, "unrecognized function name") || strings.Contains(lower, "unsupported custom tool call") || strings.Contains(lower, "unknown tool") || text == "unsupported call: "+call.Tool.Name || text == "unsupported call: "+call.Tool.Namespace+"."+call.Tool.Name
	case "escalation-denied":
		passed = strings.Contains(lower, "approval policy") && strings.Contains(lower, "never")
	case "running":
		passed = strings.Contains(text, "Process running with session ID")
	case "fixture":
		passed = strings.Contains(text, `"environment_ok":true`) && strings.Contains(text, `"network_denied":true`) && strings.Contains(text, `"checks_ok":true`)
	case "mcp-dispatch":
		passed = strings.Contains(text, "MCP tool call requires approval, but approval policy is never")
	case "plan":
		passed = strings.TrimSpace(text) == "Plan updated"
	case "canary-discovery":
		_, err := advertisedReplayTools(output)
		passed = err == nil
	}
	if !passed {
		return fmt.Errorf("replay limitation: %s did not prove %s through %s", call.ID, call.Want, call.Tool.Name)
	}
	return nil
}

// A real, task-owned Streamable HTTP MCP capability supplies the paired control.
// Its one tool returns fixed synthetic text and has no filesystem authority.
type replayMCP struct {
	mu     sync.Mutex
	listed int
	called int
	err    error
}

func (m *replayMCP) ServeHTTP(w http.ResponseWriter, request *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if request.Method != "POST" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	data, err := io.ReadAll(io.LimitReader(request.Body, 16385))
	var rpc struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
	}
	if err != nil || len(data) > 16384 || json.Unmarshal(data, &rpc) != nil {
		m.err = errors.New("MCP canary request malformed")
		http.Error(w, "invalid canary request", 400)
		return
	}
	if len(rpc.ID) == 0 {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	var result any
	switch rpc.Method {
	case "initialize":
		result = map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]string{"name": "orch-synthetic-canary", "version": "1"}}
	case "tools/list":
		m.listed++
		result = map[string]any{"tools": []any{map[string]any{"name": "read_synthetic", "description": "Return fixed synthetic canary evidence.", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}}}}
	case "tools/call":
		m.called++
		result = map[string]any{"content": []any{map[string]string{"type": "text", "text": "synthetic MCP canary reached"}}, "isError": false}
	case "resources/list":
		result = map[string]any{"resources": []any{}}
	case "resources/templates/list":
		result = map[string]any{"resourceTemplates": []any{}}
	default:
		m.err = errors.New("MCP canary received unsupported method")
		http.Error(w, "unexpected canary method", 400)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": rpc.ID, "result": result}); err != nil {
		m.err = errors.New("MCP canary response failed")
	}
}

func verifyReplayFiles(preserved map[string][32]byte, forbidden []string) error {
	for path, want := range preserved {
		data, err := os.ReadFile(path)
		if err != nil || sha256.Sum256(data) != want {
			return errors.New("replay limitation: protected synthetic content changed or disappeared")
		}
	}
	for _, path := range forbidden {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			return errors.New("replay limitation: forbidden synthetic output exists or cannot be checked")
		}
	}
	return nil
}

type replayFixtureRequest struct {
	Scratch string `json:"scratch"`
	Network string `json:"network"`
	Probes  []struct {
		Dir   string `json:"dir"`
		Read  bool   `json:"read"`
		Write bool   `json:"write"`
	} `json:"probes"`
	Marker string `json:"marker"`
}

// The test binary performs the same bounded synthetic probes through both
// exec_command and a live write_stdin session. No file contents are output.
func modelToolFixture(args []string) int {
	if len(args) != 2 {
		return 70
	}
	data, err := base64.RawURLEncoding.DecodeString(args[1])
	var request replayFixtureRequest
	if err != nil || len(data) > 16384 || json.Unmarshal(data, &request) != nil || len(request.Probes) == 0 || len(request.Probes) > 16 {
		return 71
	}
	if args[0] == "stdin" {
		// Bound even a failed caller/transport; never leave a waiting fixture behind.
		time.AfterFunc(15*time.Second, func() { os.Exit(72) })
		_, _ = fmt.Fprintln(os.Stdout, "orch-replay-ready")
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil || strings.TrimRight(line, "\r\n") != "run" {
			return 73
		}
	} else if args[0] != "direct" {
		return 74
	}
	checks := true
	for _, probe := range request.Probes {
		if !filepath.IsAbs(probe.Dir) {
			return 75
		}
		_, err := os.ReadFile(filepath.Join(probe.Dir, "sentinel"))
		checks = checks && ((probe.Read && err == nil) || (!probe.Read && errors.Is(err, os.ErrPermission)))
		for _, name := range []string{"sentinel", "shell-forbidden-new"} {
			err := os.WriteFile(filepath.Join(probe.Dir, name), []byte("written"), 0o600)
			checks = checks && ((probe.Write && err == nil) || (!probe.Write && errors.Is(err, os.ErrPermission)))
		}
	}
	environment := true
	var unexpected []string
	for _, name := range []string{"ORCH_CONTROLLER_CREDENTIAL", "OPENAI_API_KEY", "GH_TOKEN", "GITHUB_TOKEN", "CODEX_HOME", "HOME", "USERPROFILE", "LOCALAPPDATA", "APPDATA"} {
		environment = environment && os.Getenv(name) == ""
		if os.Getenv(name) != "" {
			unexpected = append(unexpected, name)
		}
	}
	for _, name := range []string{"TEMP", "TMP", "TMPDIR"} {
		environment = environment && strings.EqualFold(os.Getenv(name), request.Scratch)
		if !strings.EqualFold(os.Getenv(name), request.Scratch) {
			unexpected = append(unexpected, name+"_NOT_SCRATCH")
		}
	}
	connection, networkErr := net.DialTimeout("tcp", request.Network, time.Second)
	if connection != nil {
		_ = connection.Close()
	}
	if err := os.WriteFile(request.Marker, []byte("executed"), 0o600); err != nil {
		return 76
	}
	if err := json.NewEncoder(os.Stdout).Encode(map[string]any{"checks_ok": checks, "environment_ok": environment, "network_denied": networkErr != nil, "unexpected_env_names": unexpected}); err != nil {
		return 77
	}
	return 0
}

func TestToolReplayEvidenceFailsClosed(t *testing.T) {
	for _, raw := range []string{"null", "[]", `[{"type":"web_search"}]`, `[{"type":"function","name":"x"},{"type":"function","name":"x"}]`, `[{"type":"namespace","name":"functions","tools":null}]`} {
		if _, err := advertisedReplayTools(json.RawMessage(raw)); err == nil {
			t.Fatalf("invalid tool inventory accepted: %s", raw)
		}
	}
	call := replayCall{ID: "proof", Tool: replayTool{Name: "apply_patch", Kind: "custom"}}
	for _, missing := range []string{"execution", "output", "cleanup", "reply"} {
		r := toolReplay{requests: 2, stages: 1, calls: []replayCall{call}, outputs: map[string]json.RawMessage{"proof": json.RawMessage(`"native output"`)}}
		var cleanup error
		switch missing {
		case "execution":
			r.requests = 0
		case "output":
			delete(r.outputs, "proof")
		case "cleanup":
			cleanup = errors.New("synthetic cleanup failure")
		case "reply":
			r.err = ErrMalformedMessage
		}
		if err := r.validate(cleanup); err == nil {
			t.Fatalf("missing %s evidence accepted", missing)
		}
	}
	path := filepath.Join(t.TempDir(), "synthetic-protected")
	if err := os.WriteFile(path, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	preserved := map[string][32]byte{path: sha256.Sum256([]byte("preserve"))}
	if err := verifyReplayFiles(preserved, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := verifyReplayFiles(preserved, nil); err == nil {
		t.Fatal("protected change accepted")
	}
	if err := verifyReplayFiles(nil, []string{path}); err == nil {
		t.Fatal("forbidden creation accepted")
	}
}
