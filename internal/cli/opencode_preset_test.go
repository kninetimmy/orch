package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kninetimmy/orch/internal/config"
	"github.com/kninetimmy/orch/internal/execx"
	"github.com/kninetimmy/orch/internal/question"
)

const deepSeekCatalogModels = `
{"id":"deepseek-v4.1-flash","providerID":"opencode-go","enabled":true,"capabilities":{"tools":true,"input":["text"],"output":["text"]},"variants":[{"id":"max"}]},
{"id":"glm-5.3-flash","providerID":"opencode-go","enabled":true,"capabilities":{"tools":true,"input":["text"],"output":["text"]},"variants":[{"id":"high"}]},
{"id":"gpt-6.1-sol","providerID":"openai","enabled":true,"capabilities":{"tools":true,"input":["text"],"output":["text"]},"variants":[{"id":"high"},{"id":"xhigh"}]}`

// Exercise real CLI detection, AnswerSet decoding, proposal, approval and the
// re-detection at each application endpoint. Init/configure stop at the existing
// dirty-tree preflight after successful re-derivation: no issue/PR is published.
// Configure-local exercises the successful on-disk application too.
func TestOpenCodeDeepSeekPresetCLIApplicationBoundaries(t *testing.T) {
	for _, runtime := range []string{"opencode2", "opencode"} {
		for _, kind := range []string{"init", "configure", "configure-local"} {
			t.Run(runtime+"/"+kind, func(t *testing.T) {
				env, _, _ := testEnv(t)
				env.LookPath = func(name string) (string, error) {
					if name == runtime || name == "git" || name == "gh" {
						return "/fake/" + name, nil
					}
					return "", fmt.Errorf("%s not found", name)
				}
				statusCalled := false
				runner := fakeRunner{
					toplevel: env.RepoRoot, opencodeVersion: runtime + " v2.0.0",
					opencodeCatalog: openCodeCatalogResponse(env.RepoRoot, deepSeekCatalogModels),
					beforeRun: func(c execx.Cmd) {
						if c.Name == "git" && len(c.Args) > 0 && c.Args[0] == "status" {
							statusCalled = true
						}
					},
				}
				if runtime == "opencode" {
					runner.opencodeVersionErr = errors.New("opencode2 not found")
				}
				env.Runner = runner
				var original []byte
				if kind != "init" {
					writeConfig(t, env.RepoRoot, mixedOpenCodeTOML)
					original = []byte(mixedOpenCodeTOML)
				}
				req := question.AnswerSet{SchemaVersion: question.SchemaVersion, Answers: map[string]string{}}
				invoke := func(flag string) (int, string, string) {
					data, err := json.Marshal(req)
					if err != nil {
						t.Fatal(err)
					}
					var stdout, stderr bytes.Buffer
					env.Stdin, env.Stdout, env.Stderr = bytes.NewReader(data), &stdout, &stderr
					code := Run([]string{kind, flag}, env)
					return code, stdout.String(), stderr.String()
				}
				presetSeen := false
				var proposal string
				for step := 0; step < 100 && proposal == ""; step++ {
					code, out, stderr := invoke("--step")
					if code != ExitOK {
						t.Fatalf("step: exit=%d stderr=%s", code, stderr)
					}
					var doc question.Document
					if err := json.Unmarshal([]byte(out), &doc); err != nil {
						t.Fatal(err)
					}
					switch doc.Kind {
					case question.DocQuestions:
						for _, q := range doc.Questions {
							req.Answers[q.ID] = q.Default
							switch q.ID {
							case "pick.roles.opencode", "pick.opencode":
								req.Answers[q.ID] = "yes"
							case "opencode.preset.deepseek":
								if q.Default != "no" {
									t.Fatal("preset selected automatically")
								}
								presetSeen = true
								req.Answers[q.ID] = "yes"
							}
						}
					case question.DocSummary:
						if len(doc.Summary.Blockers) != 0 {
							t.Fatalf("proposal blocked: %v", doc.Summary.Blockers)
						}
						proposal = doc.Summary.ConfigTOML
					default:
						t.Fatalf("unexpected document %s", doc.Kind)
					}
				}
				if !presetSeen || proposal == "" {
					t.Fatal("preset interview did not reach proposal")
				}
				t.Logf("proposed configuration:\n%s", proposal)
				applyFlag := "--apply"
				if kind == "init" {
					applyFlag = "--bootstrap"
				} else if kind == "configure" {
					applyFlag = "--deliver"
				}
				// Application requires explicit final approval, not just a preset.
				for _, approval := range []string{"", "abort"} {
					if approval != "" {
						req.Answers["approval"] = approval
					}
					code, _, stderr := invoke(applyFlag)
					if code != ExitError || statusCalled || !strings.Contains(stderr, "not") {
						t.Fatalf("unapproved application: exit=%d status=%t stderr=%s", code, statusCalled, stderr)
					}
				}
				req.Answers["approval"] = "approve"
				// Catalog loss or an exact-variant change is rechecked at apply.
				for _, models := range []string{"", strings.ReplaceAll(deepSeekCatalogModels, `{"id":"xhigh"}`, `{"id":"other"}`)} {
					changed := runner
					changed.opencodeCatalog = openCodeCatalogResponse(env.RepoRoot, models)
					env.Runner = changed
					code, _, stderr := invoke(applyFlag)
					if code != ExitError || statusCalled || (!strings.Contains(stderr, "catalog") && !strings.Contains(stderr, "preset")) {
						t.Fatalf("changed catalog application: exit=%d status=%t stderr=%s", code, statusCalled, stderr)
					}
				}
				env.Runner = runner
				code, out, stderr := invoke("--step")
				var complete question.Document
				if err := json.Unmarshal([]byte(out), &complete); err != nil || code != ExitOK || complete.Kind != question.DocComplete || complete.Complete.Summary.ConfigTOML != proposal {
					t.Fatalf("approval: exit=%d doc=%+v err=%v stderr=%s", code, complete, err, stderr)
				}
				code, _, stderr = invoke(applyFlag)
				if kind == "configure-local" {
					if code != ExitOK {
						t.Fatalf("apply: exit=%d stderr=%s", code, stderr)
					}
					raw, err := os.ReadFile(filepath.Join(env.RepoRoot, filepath.FromSlash(config.LocalOverridePath)))
					if err != nil || string(raw) != proposal {
						t.Fatalf("applied local configuration differs: %s, err=%v", raw, err)
					}
				} else if code != ExitError || !statusCalled {
					t.Fatalf("expected successful re-derivation to reach dirty-tree preflight: exit=%d status=%t stderr=%s", code, statusCalled, stderr)
				}
				path := filepath.Join(env.RepoRoot, filepath.FromSlash(config.Path))
				raw, err := os.ReadFile(path)
				if kind == "init" {
					if !os.IsNotExist(err) {
						t.Fatalf("bootstrap unexpectedly wrote configuration: %v", err)
					}
				} else if err != nil || !bytes.Equal(raw, original) {
					t.Fatalf("shared configuration changed: %s, err=%v", raw, err)
				}
			})
		}
	}
}
