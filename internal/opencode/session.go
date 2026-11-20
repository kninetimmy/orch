package opencode

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/kninetimmy/orch/internal/execx"
)

var sessionIDPattern = regexp.MustCompile(`^ses[A-Za-z0-9_-]{1,125}$`)

// SessionDirectory observes the invoking local OpenCode service's session
// location. sessionID is only a lookup key. Never start/discover a replacement
// service, query a caller-selected endpoint, request a directory, or use an
// outer response location (which can just reflect the server's defaults).
func SessionDirectory(ctx context.Context, runner execx.Runner, cwd, sessionID string) (string, error) {
	const prefix = "OpenCode session workspace cannot be verified: "
	if !sessionIDPattern.MatchString(sessionID) {
		return "", fmt.Errorf("%smissing or invalid session metadata", prefix)
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	runtime, err := Resolve(ctx, runner, cwd)
	if err != nil {
		return "", fmt.Errorf("%s%w", prefix, err)
	}
	res, err := runner.Run(ctx, execx.Cmd{Name: runtime.Executable, Args: []string{"debug", "paths", "state"}, Dir: cwd})
	state := strings.TrimSpace(res.Stdout)
	if err != nil || res.ExitCode != 0 || !filepath.IsAbs(state) || strings.ContainsAny(state, "\r\n\x00") {
		return "", fmt.Errorf("%sOpenCode service state directory is unavailable", prefix)
	}
	var service struct {
		PID      int    `json:"pid"`
		URL      string `json:"url"`
		Password string `json:"password"`
	}
	file := filepath.Join(state, "service.json")
	info, err := os.Stat(file)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 16<<10 {
		return "", fmt.Errorf("%sno usable local OpenCode service registration", prefix)
	}
	data, err := os.ReadFile(file)
	if err != nil || json.Unmarshal(data, &service) != nil || service.PID <= 0 || service.PID != os.Getppid() || service.Password == "" {
		return "", fmt.Errorf("%sregistration is not for the OpenCode process that launched this stdio server", prefix)
	}
	endpoint, err := url.Parse(service.URL)
	if err != nil || endpoint.Scheme != "http" || endpoint.User != nil || endpoint.RawQuery != "" ||
		endpoint.ForceQuery || endpoint.Fragment != "" || endpoint.RawPath != "" || (endpoint.Path != "" && endpoint.Path != "/") {
		return "", fmt.Errorf("%sservice endpoint is not local loopback HTTP", prefix)
	}
	port, err := strconv.ParseUint(endpoint.Port(), 10, 16)
	if ip := net.ParseIP(endpoint.Hostname()); ip == nil || !ip.IsLoopback() || err != nil || port == 0 {
		return "", fmt.Errorf("%sservice endpoint is not local loopback HTTP", prefix)
	}

	client := &http.Client{
		Transport:     &http.Transport{DisableKeepAlives: true}, // No environment proxy.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	get := func(path string, out any) bool {
		endpoint.Path = path
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
		if err != nil {
			return false
		}
		req.SetBasicAuth("opencode", service.Password)
		response, err := client.Do(req)
		if err != nil {
			return false
		}
		defer func() { _ = response.Body.Close() }()
		body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20+1))
		return response.StatusCode == http.StatusOK && err == nil && len(body) <= 1<<20 && json.Unmarshal(body, out) == nil
	}
	var server struct {
		PID int `json:"pid"`
	}
	if !get("/api/info", &server) || server.PID != service.PID {
		return "", fmt.Errorf("%slocal OpenCode service identity is unavailable or changed", prefix)
	}
	var session struct {
		Data struct {
			ID       string `json:"id"`
			Location struct {
				Directory   string `json:"directory"`
				WorkspaceID string `json:"workspaceID"`
			} `json:"location"`
		} `json:"data"`
	}
	if !get("/api/session/"+sessionID, &session) || session.Data.ID != sessionID ||
		!filepath.IsAbs(session.Data.Location.Directory) || session.Data.Location.WorkspaceID != "" {
		return "", fmt.Errorf("%sOpenCode did not return the invoking session's local directory", prefix)
	}
	return session.Data.Location.Directory, nil
}
