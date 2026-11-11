// A scripted executable copied under doctor's tool names. It accepts only the
// fixture's exact read probes and logs outside the repository under test.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	code, err := respond()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(99)
	}
	os.Exit(code)
}

func respond() (int, error) {
	data, err := os.ReadFile(os.Getenv("ORCH_TEST_DOCTOR_FIXTURE"))
	if err != nil {
		return 0, err
	}
	var fixture struct {
		Root, Log string
		Probes    map[string]struct {
			Stdout, Stderr string
			ExitCode       int
		}
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		return 0, err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return 0, err
	}
	command := strings.TrimSuffix(filepath.Base(os.Args[0]), ".exe") + " " + strings.Join(os.Args[1:], " ")
	log, err := os.OpenFile(fixture.Log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, err
	}
	_, writeErr := fmt.Fprintf(log, "%s\t%s\n", cwd, command)
	if err := log.Close(); err != nil {
		return 0, err
	}
	if writeErr != nil {
		return 0, writeErr
	}
	rootInfo, err := os.Stat(fixture.Root)
	if err != nil {
		return 0, fmt.Errorf("stat fixture repository %q: %w", fixture.Root, err)
	}
	dirInfo, err := os.Stat(cwd)
	if err != nil {
		return 0, fmt.Errorf("stat probe directory %q: %w", cwd, err)
	}
	if !os.SameFile(rootInfo, dirInfo) {
		return 0, fmt.Errorf("wrong repository: %s, want %s", cwd, fixture.Root)
	}
	probe, ok := fixture.Probes[command]
	if !ok {
		return 0, fmt.Errorf("forbidden command: %s", command)
	}
	if _, err := fmt.Fprint(os.Stdout, probe.Stdout); err != nil {
		return 0, err
	}
	if _, err := fmt.Fprint(os.Stderr, probe.Stderr); err != nil {
		return 0, err
	}
	return probe.ExitCode, nil
}
