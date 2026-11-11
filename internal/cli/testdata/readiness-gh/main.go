// A deterministic gh executable for real CLI/MCP stdio tests. Every accepted
// command is a read; any attempted mutation is logged and fails explicitly.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

func main() {
	if err := respond(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func respond() error {
	data, err := os.ReadFile(os.Getenv("ORCH_TEST_GH_FIXTURE"))
	if err != nil {
		return err
	}
	var fixture struct {
		Root     string
		Log      string
		PR       json.RawMessage
		Issue    json.RawMessage
		Rollup   json.RawMessage
		Checks   json.RawMessage
		Fail     string
		FailText string
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	args := strings.Join(os.Args[1:], " ")
	log, err := os.OpenFile(fixture.Log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := fmt.Fprintf(log, "%s\t%s\n", cwd, args)
	if err := log.Close(); err != nil {
		return err
	}
	if writeErr != nil {
		return writeErr
	}
	if !strings.EqualFold(cwd, fixture.Root) {
		return fmt.Errorf("wrong repository: %s, want %s", cwd, fixture.Root)
	}
	if fixture.Fail != "" && strings.Contains(args, fixture.Fail) {
		return fmt.Errorf("scripted read probe failed: %s", fixture.FailText)
	}
	var out json.RawMessage
	switch args {
	case "auth status":
		return nil
	case "pr view 10 --json number,state,title,url,headRefName,baseRefName,headRefOid,mergeStateStatus,mergedAt,body":
		out = fixture.PR
	case "issue view 1 --json number,state,title,url,labels,body":
		out = fixture.Issue
	case "pr view 10 --json statusCheckRollup":
		out = fixture.Rollup
	case "pr checks 10 --required --json name,state,bucket,link":
		out = fixture.Checks
	default:
		return fmt.Errorf("forbidden command: gh %s", args)
	}
	_, err = fmt.Fprintln(os.Stdout, string(out))
	return err
}
