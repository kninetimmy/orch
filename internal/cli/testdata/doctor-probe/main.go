// A scripted executable copied under tool names. Before activation/dispatch
// tests reused it, fixtures contained only doctor's exact read probes. It still
// accepts only explicitly scripted commands and logs outside the tested repo.
package main

import (
	"encoding/json"
	"fmt"
	"io"
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
			Dir            string // Optional exact directory; defaults to Root.
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
	tool := strings.TrimSuffix(filepath.Base(os.Args[0]), ".exe")
	command := tool + " " + strings.Join(os.Args[1:], " ")
	if tool == "gh" && len(os.Args) > 2 && os.Args[1] == "api" && os.Args[2] == "graphql" {
		var request struct{ OperationName string }
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return 0, err
		}
		if err := json.Unmarshal(b, &request); err != nil || request.OperationName == "" {
			return 0, fmt.Errorf("missing or malformed fixture GraphQL operation: %v", err)
		}
		command += " " + request.OperationName
	}
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
	probe, ok := fixture.Probes[command]
	if !ok {
		return 0, fmt.Errorf("forbidden command: %s", command)
	}
	root := fixture.Root
	if probe.Dir != "" {
		root = probe.Dir
	}
	rootInfo, err := os.Stat(root)
	if err != nil {
		return 0, fmt.Errorf("stat fixture directory %q: %w", root, err)
	}
	dirInfo, err := os.Stat(cwd)
	if err != nil {
		return 0, fmt.Errorf("stat probe directory %q: %w", cwd, err)
	}
	if !os.SameFile(rootInfo, dirInfo) {
		return 0, fmt.Errorf("wrong directory: %s, want %s", cwd, root)
	}
	if _, err := fmt.Fprint(os.Stdout, probe.Stdout); err != nil {
		return 0, err
	}
	if _, err := fmt.Fprint(os.Stderr, probe.Stderr); err != nil {
		return 0, err
	}
	return probe.ExitCode, nil
}
