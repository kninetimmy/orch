package codexnative

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/kninetimmy/orch/internal/paths"
)

// Test-only caller assertion, not a new production approval or configuration API.
type subscriptionTrial struct {
	Caller         string
	Options        Options
	Task           Task
	Scenario       string
	TimeoutSeconds int
	ApprovedAt     time.Time
	ExpiresAt      time.Time
	Preserved      map[string]string // exact parent sentinel SHA-256 hashes
	Expected       map[string]string // exact task-owned output hashes; required for completion
	Forbidden      []string
	Evidence       string // one-use JSONL claim and result under protected controller state
	Assertion      string
}

func (trial subscriptionTrial) assertion() string {
	trial.Assertion = ""
	data, _ := json.Marshal(trial)
	return fmt.Sprintf("I authorize one managed ChatGPT native trial: sha256:%x", sha256.Sum256(data))
}

func (trial subscriptionTrial) validate(now time.Time) (*Session, error) {
	if trial.Caller != "TestCodexSubscriptionTrial" || trial.Assertion != trial.assertion() {
		return nil, errors.New("subscription trial requires an exact caller/task/profile authorization assertion")
	}
	if trial.ApprovedAt.IsZero() || trial.ApprovedAt.After(now) || !trial.ExpiresAt.After(now) || trial.ExpiresAt.Sub(trial.ApprovedAt) > 30*time.Minute || trial.TimeoutSeconds < 1 || trial.TimeoutSeconds > 180 || now.Add(time.Duration(trial.TimeoutSeconds)*time.Second).After(trial.ExpiresAt) {
		return nil, errors.New("subscription trial authorization expired or finite limits invalid")
	}
	if trial.Scenario != "complete" && trial.Scenario != "interrupt" && trial.Scenario != "recover" {
		return nil, errors.New("subscription trial scenario unsupported")
	}
	s, _, err := newSession(trial.Options, trial.Task)
	if err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(s.task, trial.Task) || s.options != trial.Options || !filepath.IsAbs(trial.Options.Executable) {
		return nil, errors.New("subscription trial requires exact canonical task/options and installed executable")
	}
	inside, err := paths.Inside(s.task.Layout.ControllerState, trial.Evidence)
	canonical, pathErr := isolationPath(trial.Evidence, false)
	if err != nil || pathErr != nil || !inside || canonical != trial.Evidence || filepath.Ext(trial.Evidence) != ".jsonl" {
		return nil, errors.New("subscription trial evidence must be canonical and protected")
	}
	if len(trial.Preserved) == 0 || len(trial.Preserved) > 16 || len(trial.Forbidden) > 32 || len(trial.Expected) > 16 || (trial.Scenario == "complete" && len(trial.Expected) == 0) {
		return nil, errors.New("subscription trial requires bounded parent checks")
	}
	for path, hash := range trial.Preserved {
		decoded, err := hex.DecodeString(hash)
		if err != nil || len(decoded) != sha256.Size || trial.parentPath(s, path) != nil || path == trial.Evidence {
			return nil, errors.New("subscription trial parent sentinel binding invalid")
		}
	}
	for _, path := range trial.Forbidden {
		if trial.parentPath(s, path) != nil || path == trial.Evidence {
			return nil, errors.New("subscription trial forbidden path binding invalid")
		}
	}
	for path, hash := range trial.Expected {
		decoded, err := hex.DecodeString(hash)
		if err != nil || len(decoded) != sha256.Size {
			return nil, errors.New("subscription trial expected output hash invalid")
		}
		if _, err := trial.expectedRoot(s, path); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (trial subscriptionTrial) expectedRoot(s *Session, path string) (string, error) {
	canonical, err := isolationPath(path, false)
	if err != nil || canonical != path {
		return "", errors.New("subscription trial expected output path changed or unverifiable")
	}
	for _, root := range []string{s.task.Layout.Workspace, s.task.Layout.Scratch} {
		if inside, err := paths.Inside(root, path); err != nil {
			return "", err
		} else if inside {
			return root, nil
		}
	}
	return "", errors.New("subscription trial expected output must be in workspace/scratch")
}

func (trial subscriptionTrial) checkExpected(s *Session) error {
	for path, hash := range trial.Expected {
		root, err := trial.expectedRoot(s, path) // revalidate after worker changes
		if err != nil {
			return err
		}
		if err := checkTrialHash(root, path, hash); err != nil {
			return err
		}
	}
	return nil
}

func checkTrialHash(root, path, expected string) (err error) {
	canonical, err := isolationPath(path, false)
	if err != nil || canonical != path {
		return errors.New("subscription trial checked path changed or unverifiable")
	}
	dir, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, dir.Close()) }()
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return err
	}
	info, err := dir.Lstat(relative)
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("subscription trial checked file unavailable or linked")
	}
	file, err := dir.Open(relative) // Root.Open contains resolution even if a link is replaced
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) || !trialSingleLink(file) {
		return errors.New("subscription trial checked file identity changed")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxMessageBytes+1))
	if err != nil || len(data) > maxMessageBytes || fmt.Sprintf("%x", sha256.Sum256(data)) != expected {
		return errors.New("subscription trial checked file changed or unverifiable")
	}
	return nil
}

func (trial subscriptionTrial) parentPath(s *Session, path string) error {
	canonical, err := isolationPath(path, false)
	if err != nil || canonical != path {
		return errors.New("parent check requires a canonical path")
	}
	allowed := false
	for _, root := range append([]string{s.task.Layout.MainCheckout, s.task.Layout.ControllerState}, s.task.Layout.SiblingWorkspaces...) {
		inside, err := paths.Inside(root, path)
		if err != nil {
			return err
		}
		allowed = allowed || inside
	}
	if !allowed {
		return errors.New("parent checks require approved protected sentinels")
	}
	credentials := append([]string(nil), s.task.Layout.CredentialPaths...)
	for _, name := range []string{"CODEX_HOME", "HOME", "USERPROFILE"} {
		if root := os.Getenv(name); root != "" {
			if name != "CODEX_HOME" {
				root = filepath.Join(root, ".codex")
			}
			credentials = append(credentials, root)
		}
	}
	for _, root := range credentials {
		inside, err := paths.Inside(root, path)
		if err != nil || inside {
			return errors.New("parent checks cannot probe credential locations")
		}
	}
	return nil
}

func (trial subscriptionTrial) claim(now time.Time) (*Session, *os.File, error) {
	s, err := trial.validate(now)
	if err != nil {
		return nil, nil, err
	}
	file, err := os.OpenFile(trial.Evidence, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, nil, fmt.Errorf("subscription trial claim unavailable or already consumed: %w", err)
	}
	return s, file, nil
}

func TestSubscriptionTrialAuthorization(t *testing.T) {
	options, task := sessionTask(t)
	s, _, err := newSession(options, task)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	sentinel := filepath.Join(s.task.Layout.MainCheckout, "trial-sentinel")
	trial := subscriptionTrial{Caller: "TestCodexSubscriptionTrial", Options: s.options, Task: s.task, Scenario: "complete", TimeoutSeconds: 30,
		ApprovedAt: now, ExpiresAt: now.Add(5 * time.Minute), Evidence: filepath.Join(s.task.Layout.ControllerState, "trial.jsonl"),
		Preserved: map[string]string{sentinel: fmt.Sprintf("%x", sha256.Sum256([]byte("preserve")))}}
	trial.Expected = map[string]string{filepath.Join(s.task.Layout.Scratch, "trial-output"): fmt.Sprintf("%x", sha256.Sum256([]byte("written")))}
	trial.Assertion = trial.assertion()
	for _, edit := range []func(*subscriptionTrial){
		func(v *subscriptionTrial) { v.Assertion = "" },
		func(v *subscriptionTrial) { v.Task.Prompt += "different task" },
		func(v *subscriptionTrial) { v.Task.Selection.Effort = "high" },
		func(v *subscriptionTrial) { v.Caller = "another caller"; v.Assertion = v.assertion() },
		func(v *subscriptionTrial) { v.ExpiresAt = now.Add(-time.Second); v.Assertion = v.assertion() },
		func(v *subscriptionTrial) { v.TimeoutSeconds = 181; v.Assertion = v.assertion() },
		func(v *subscriptionTrial) { v.Scenario = "unbounded"; v.Assertion = v.assertion() },
		func(v *subscriptionTrial) { v.Expected = nil; v.Assertion = v.assertion() },
		func(v *subscriptionTrial) { v.Expected = v.Preserved; v.Assertion = v.assertion() },
		func(v *subscriptionTrial) {
			v.Evidence = filepath.Join(v.Task.Layout.Workspace, "trial.jsonl")
			v.Assertion = v.assertion()
		},
		func(v *subscriptionTrial) {
			v.Preserved = map[string]string{filepath.Join(v.Task.Layout.CredentialPaths[0], "sentinel"): trial.Preserved[sentinel]}
			v.Assertion = v.assertion()
		},
	} {
		changed := trial
		edit(&changed)
		if _, file, err := changed.claim(now); err == nil || file != nil {
			t.Fatal("missing/mismatched/expired/unsafe authorization reached trial claim")
		}
	}
	_, file, err := trial.claim(now)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, file, err := trial.claim(now); err == nil || file != nil {
		t.Fatal("reused trial authorization accepted")
	}
	if _, err := os.Stat(filepath.Join(task.Layout.Workspace, ".scripted-native-calls")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("authorization checks launched a host")
	}
}

func TestSubscriptionTrialExpectedOutput(t *testing.T) {
	options, task := sessionTask(t)
	s, _, err := newSession(options, task)
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(s.task.Layout.Workspace, "result")
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte("written")))
	trial := subscriptionTrial{Expected: map[string]string{output: hash}}
	if trial.checkExpected(s) == nil {
		t.Fatal("model completion without the requested tool output accepted")
	}
	if err := os.WriteFile(output, []byte("written"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := trial.checkExpected(s); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(s.task.Layout.Workspace, "alias")
	aliasedOutput := filepath.Join(alias, "result")
	trial.Expected = map[string]string{aliasedOutput: hash}
	if _, err := trial.expectedRoot(s, aliasedOutput); err != nil {
		t.Fatal(err) // approved output path before model-created alias
	}
	if err := os.WriteFile(filepath.Join(s.task.Layout.MainCheckout, "result"), []byte("written"), 0o600); err != nil {
		t.Fatal(err)
	}
	linkIsolationDirectory(t, alias, s.task.Layout.MainCheckout)
	if trial.checkExpected(s) == nil {
		t.Fatal("parent followed replaced output alias into protected source")
	}
	linkedOutput := filepath.Join(s.task.Layout.Workspace, "linked-result")
	if err := os.Link(filepath.Join(s.task.Layout.MainCheckout, "result"), linkedOutput); err != nil {
		t.Fatal(err)
	}
	trial.Expected = map[string]string{linkedOutput: hash}
	if trial.checkExpected(s) == nil {
		t.Fatal("parent accepted a hard-linked protected source as tool output")
	}
}
