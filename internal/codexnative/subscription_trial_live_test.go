//go:build codex_subscription

package codexnative

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// Never selected by ordinary CI or the codex_live synthetic gates. The caller
// must separately approve the exact binding; this test neither routes nor approves.
func TestCodexSubscriptionTrial(t *testing.T) {
	trial := readSubscriptionTrial(t)
	if runtime.GOOS != "windows" {
		t.Fatal("subscription trial refused: unsupported platform")
	}
	s, evidence, err := trial.claim(time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := runSubscriptionTrial(trial, s, evidence); err != nil {
		t.Fatal(err)
	}
}

// Preparation prints the assertion for a separate human gate; no host or claim.
func TestCodexSubscriptionTrialAuthorization(t *testing.T) {
	trial := readSubscriptionTrial(t)
	trial.Assertion = trial.assertion()
	if _, err := trial.validate(time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	t.Logf("Required separate human authorization: %s", trial.Assertion)
}

func readSubscriptionTrial(t *testing.T) subscriptionTrial {
	t.Helper()
	path := os.Getenv("ORCH_CODEX_SUBSCRIPTION_TRIAL")
	if !filepath.IsAbs(path) || filepath.Base(path) != "codex-subscription-authorization.json" {
		t.Fatal("subscription trial refused: exact authorization file required")
	}
	canonical, err := isolationPath(path, false)
	if err != nil || canonical != path {
		t.Fatal("subscription trial refused: authorization path must be canonical")
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal("subscription trial refused: authorization unavailable")
	}
	var trial subscriptionTrial
	data, readErr := io.ReadAll(io.LimitReader(file, 65537))
	if err := errors.Join(readErr, file.Close()); err != nil || len(data) > 65536 {
		t.Fatal("subscription trial refused: authorization read failed or exceeds 64 KiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	err = decoder.Decode(&trial)
	if err == nil {
		var extra any
		if decoder.Decode(&extra) != io.EOF {
			err = errors.New("extra authorization data")
		}
	}
	if err != nil {
		t.Fatal("subscription trial refused: invalid authorization")
	}
	return trial
}

func runSubscriptionTrial(trial subscriptionTrial, s *Session, evidence *os.File) (err error) {
	encoder := json.NewEncoder(evidence)
	var before *SessionResult
	initialError := ""
	var expectedChecksPassed *bool
	defer func() {
		parentErr := trial.checkParent()
		err = errors.Join(err, parentErr)
		detail := ""
		if err != nil {
			detail = err.Error()
		}
		err = errors.Join(err, encoder.Encode(map[string]any{"finishedAt": time.Now().UTC(), "initialError": initialError, "beforeResume": before, "result": s.Result(), "parentChecksPassed": parentErr == nil, "expectedChecksPassed": expectedChecksPassed, "error": detail}), evidence.Sync(), evidence.Close())
	}()
	if err := encoder.Encode(map[string]any{"startedAt": time.Now().UTC(), "authorization": trial}); err != nil {
		return err
	}
	if err := evidence.Sync(); err != nil {
		return err
	}
	if err := trial.checkParent(); err != nil {
		return err
	}
	for path := range trial.Expected {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			return errors.New("subscription trial expected output must be absent before execution")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(trial.TimeoutSeconds)*time.Second)
	defer cancel()
	c, cleanup, err := s.connect(ctx) // same two preflights and actual-connection admission
	if err != nil {
		return s.finish(err, nil)
	}
	defer cleanup()
	if trial.Scenario != "complete" {
		ready := make(chan struct{})
		s.turnReady = ready
		go func() {
			select {
			case <-ready:
				if trial.Scenario == "interrupt" {
					cancel()
				} else {
					_ = c.stdout.Close() // owned transport loss, never a new task input
				}
			case <-ctx.Done():
			}
		}()
	}
	err = s.execute(ctx, c, false)
	if err != nil {
		initialError = err.Error()
	}
	switch trial.Scenario {
	case "complete":
		if err == nil && s.result.Outcome == SessionSuccessful {
			err := trial.checkExpected(s)
			passed := err == nil
			expectedChecksPassed = &passed
			return err
		}
	case "interrupt":
		if errors.Is(err, context.Canceled) && err.Error() == context.Canceled.Error() && s.result.Outcome == SessionCancelled && s.result.NativeStatus == "interrupted" {
			return nil
		}
	case "recover":
		checkpoint := s.Result()
		before = &checkpoint
		if s.checkResume(ctx, trial.Task) != nil || checkpoint.Outcome != SessionDisconnected {
			break
		}
		resumeErr := s.Resume(ctx, trial.Task) // same object, one reconnect, no turn/start replay
		if resumeErr == nil && s.completed && s.result.ThreadID == checkpoint.ThreadID && s.result.NativeSessionID == checkpoint.NativeSessionID && s.result.TurnID == checkpoint.TurnID {
			if s.result.Outcome == SessionSuccessful && len(trial.Expected) != 0 {
				outputErr := trial.checkExpected(s)
				passed := outputErr == nil
				expectedChecksPassed = &passed
				return outputErr
			}
			return nil // terminal interruption is also an honest same-turn recovery
		}
		err = errors.Join(err, resumeErr)
	}
	return errors.Join(errors.New("subscription trial did not validate its authorized scenario"), err)
}

func (trial subscriptionTrial) checkParent() error {
	for path, expected := range trial.Preserved {
		if err := checkTrialHash(filepath.Dir(path), path, expected); err != nil {
			return err
		}
	}
	for _, path := range trial.Forbidden {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			return errors.New("subscription trial forbidden output exists or cannot be checked")
		}
	}
	return nil
}
