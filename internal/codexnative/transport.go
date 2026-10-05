// Package codexnative implements Codex preflights and bounded task sessions.
// Production sessions require supported native enforcement and verified controls.
// Metadata connections have no execution methods.
package codexnative

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/kninetimmy/orch/internal/execx"
)

var (
	ErrMalformedMessage     = errors.New("malformed Codex app-server message")
	ErrProcessExit          = errors.New("codex app-server exited")
	ErrTransportInterrupted = errors.New("codex app-server transport interrupted")
	ErrTimeout              = errors.New("codex app-server timeout")
)

const (
	maxMessageBytes  = 1 << 20
	preflightTimeout = 15 * time.Second
	shutdownTimeout  = 2 * time.Second
)

// connection has one reader and sequential requests. A task session may also
// interrupt its outstanding turn; it never multiplexes unrelated work.
type connection struct {
	ctx             context.Context
	cmd             *exec.Cmd
	stdin           io.WriteCloser
	stdout          *os.File
	reader          *bufio.Reader
	exited          chan struct{}
	exitErr         error // read only after exited closes
	stopIO          func() bool
	sequence        int
	authSeen        bool
	isolation       bool
	diagnosticReady bool // set only after actual-connection restriction/readiness checks
	session         bool // private; enabled only after both session preflights succeed
	profile         string
	disabledMCP     []string // discovery binding, checked again before session admission
}

// start follows execx's argument-vector and explicit-cwd contract. execx.Local
// collects output until exit; streaming this small subset needs owned pipes.
func start(ctx context.Context, command execx.Cmd) (*connection, error) {
	return startWithEnv(ctx, command, append(os.Environ(), command.Env...))
}

func startWithEnv(ctx context.Context, command execx.Cmd, env []string) (*connection, error) {
	if !filepath.IsAbs(command.Dir) {
		return nil, errors.New("codex app-server requires an absolute working directory")
	}
	path, err := exec.LookPath(command.Name)
	if err != nil {
		return nil, fmt.Errorf("codex executable not found; install it or adjust PATH: %w", err)
	}
	cmd := exec.CommandContext(ctx, path, command.Args...)
	cmd.Dir = command.Dir
	cmd.Env = env
	cmd.Stderr = io.Discard // never retain account/configuration diagnostics
	cmd.WaitDelay = shutdownTimeout
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("codex app-server stdin: %w", err)
	}
	// Own stdout rather than StdoutPipe: Wait must not close it before a final
	// response has been read. The child receives only the write end.
	stdout, output, err := os.Pipe()
	if err != nil {
		return nil, errors.Join(fmt.Errorf("codex app-server stdout: %w", err), stdin.Close())
	}
	cmd.Stdout = output
	if err := cmd.Start(); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			err = errors.Join(ErrTimeout, err)
		}
		return nil, errors.Join(fmt.Errorf("start Codex app-server: %w", err), stdin.Close(), stdout.Close(), output.Close())
	}
	if err := output.Close(); err != nil {
		return nil, errors.Join(err, cmd.Process.Kill(), cmd.Wait(), stdin.Close(), stdout.Close())
	}
	c := &connection{ctx: ctx, cmd: cmd, stdin: stdin, stdout: stdout,
		reader: bufio.NewReaderSize(stdout, maxMessageBytes+1), exited: make(chan struct{})}
	c.stopIO = context.AfterFunc(ctx, func() {
		// Closing owned pipes unblocks a read/write even when a descendant has
		// inherited a pipe. CommandContext independently kills the server.
		_ = stdin.Close()
		_ = stdout.Close()
	})
	go func() {
		c.exitErr = cmd.Wait()
		close(c.exited)
	}()
	return c, nil
}

func (c *connection) contextError(operation string) error {
	if errors.Is(c.ctx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("codex app-server %s: %w", operation, errors.Join(ErrTimeout, c.ctx.Err()))
	}
	if c.ctx.Err() != nil {
		return fmt.Errorf("codex app-server %s: %w", operation, c.ctx.Err())
	}
	return nil
}

func (c *connection) exitError() error {
	return errors.Join(ErrProcessExit, c.exitErr)
}

func (c *connection) ioError(operation string, err error) error {
	if ctxErr := c.contextError(operation); ctxErr != nil {
		return ctxErr
	}
	timer := time.NewTimer(shutdownTimeout)
	defer timer.Stop()
	select {
	case <-c.exited:
		return fmt.Errorf("codex app-server %s: %w", operation, c.exitError())
	case <-c.ctx.Done():
		return c.contextError(operation)
	case <-timer.C:
		return fmt.Errorf("codex app-server %s: %w", operation, errors.Join(ErrTransportInterrupted, err))
	}
}

func (c *connection) send(value any) error {
	if err := c.contextError("write"); err != nil {
		return err
	}
	select {
	case <-c.exited:
		return c.exitError()
	default:
	}
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode Codex app-server request: %w", err)
	}
	if len(data) > maxMessageBytes {
		return fmt.Errorf("codex app-server request exceeds %d bytes", maxMessageBytes)
	}
	data = append(data, '\n')
	n, err := c.stdin.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	if err != nil {
		return c.ioError("write", err)
	}
	return nil
}

type message struct {
	ID     json.RawMessage `json:"id"`
	Method json.RawMessage `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  json.RawMessage `json:"error"`
}

type rpcRejection struct {
	method string
	code   int64
}

func (r *rpcRejection) Error() string {
	return fmt.Sprintf("codex app-server %s rejected (code %d)", r.method, r.code)
}

func (c *connection) read() (message, error) {
	if err := c.contextError("read"); err != nil {
		return message{}, err
	}
	line, err := c.reader.ReadSlice('\n')
	if errors.Is(err, bufio.ErrBufferFull) {
		return message{}, fmt.Errorf("%w: exceeds %d bytes", ErrMalformedMessage, maxMessageBytes)
	}
	if err != nil {
		if len(line) != 0 && errors.Is(err, io.EOF) {
			return message{}, fmt.Errorf("%w: unterminated JSONL message", ErrMalformedMessage)
		}
		return message{}, c.ioError("read", err)
	}
	var m message
	if err := json.Unmarshal(line, &m); err != nil {
		// Do not include raw payloads or decoder excerpts in errors.
		return m, fmt.Errorf("%w: invalid JSON object", ErrMalformedMessage)
	}
	if len(m.Method) != 0 {
		var method string
		if json.Unmarshal(m.Method, &method) != nil || method == "" || len(m.ID) != 0 || len(m.Result) != 0 || len(m.Error) != 0 {
			return m, fmt.Errorf("%w: server requests/conflicting notification fields are unsupported", ErrMalformedMessage)
		}
		if method == "account/updated" {
			var auth struct {
				Mode *string `json:"authMode"`
			}
			if err := json.Unmarshal(m.Params, &auth); err != nil {
				return m, fmt.Errorf("%w: invalid account/updated", ErrMalformedMessage)
			}
			c.authSeen = true
			if auth.Mode == nil || *auth.Mode != "chatgpt" {
				return m, errors.New("codex preflight requires managed ChatGPT authentication")
			}
		}
		return m, nil
	}
	if len(m.ID) == 0 || len(m.Params) != 0 || (len(m.Result) == 0) == (len(m.Error) == 0) {
		return m, fmt.Errorf("%w: response requires id and exactly one result/error", ErrMalformedMessage)
	}
	return m, nil
}

func (c *connection) request(method string, params any) (string, error) {
	switch method {
	case "initialize", "account/read", "model/list":
	case "config/read", "experimentalFeature/list", "windowsSandbox/readiness", "permissionProfile/list", "hooks/list", "plugin/installed":
		if !c.isolation {
			return "", errors.New("codex native preflight method unavailable")
		}
	case "command/exec":
		if !c.isolation {
			return "", errors.New("codex native preflight method unavailable")
		}
		if !c.diagnosticReady {
			return "", errors.New("codex native diagnostic command unavailable before restriction verification")
		}
	case "thread/start", "thread/resume", "turn/start", "turn/interrupt":
		if !c.isolation || !c.session {
			return "", errors.New("codex native session method unavailable")
		}
	default:
		return "", errors.New("codex native preflight method unavailable")
	}
	c.sequence++
	id := fmt.Sprintf("orch-preflight-%d", c.sequence)
	if err := c.send(struct {
		ID     string `json:"id"`
		Method string `json:"method"`
		Params any    `json:"params"`
	}{id, method, params}); err != nil {
		return "", err
	}
	return id, nil
}

func decodeResponse(m message, id, method string, result any) error {
	var responseID string
	if err := json.Unmarshal(m.ID, &responseID); err != nil || responseID != id {
		return fmt.Errorf("%w: response id does not match pending request", ErrMalformedMessage)
	}
	if len(m.Error) != 0 {
		var rejection struct {
			Code    *int64  `json:"code"`
			Message *string `json:"message"`
		}
		if err := json.Unmarshal(m.Error, &rejection); err != nil || rejection.Code == nil || rejection.Message == nil {
			return fmt.Errorf("%w: invalid RPC error", ErrMalformedMessage)
		}
		return &rpcRejection{method: method, code: *rejection.Code}
	}
	if bytes.Equal(bytes.TrimSpace(m.Result), []byte("null")) || json.Unmarshal(m.Result, result) != nil {
		return fmt.Errorf("%w: invalid %s result", ErrMalformedMessage, method)
	}
	return nil
}

func (c *connection) call(method string, params, result any) error {
	id, err := c.request(method, params)
	if err != nil {
		return err
	}
	for {
		m, err := c.read()
		if err != nil {
			return err
		}
		if len(m.Method) != 0 {
			continue // notifications never satisfy a pending request
		}
		return decodeResponse(m, id, method, result)
	}
}

func (c *connection) initialized() error {
	return c.send(struct {
		Method string `json:"method"`
	}{"initialized"})
}

func (c *connection) requireManagedAuth() error {
	for !c.authSeen {
		m, err := c.read()
		if err != nil {
			return fmt.Errorf("await managed ChatGPT authentication: %w", err)
		}
		if len(m.Method) == 0 {
			return fmt.Errorf("%w: unsolicited response while awaiting authentication", ErrMalformedMessage)
		}
	}
	return nil // every account/updated was checked by read
}

func (c *connection) close() error {
	c.stopIO()
	// Closing stdin is the stdio shutdown protocol. No shutdown RPC is sent.
	_ = c.stdin.Close()
	timer := time.NewTimer(shutdownTimeout)
	defer timer.Stop()
	var err error
	select {
	case <-c.exited:
		if c.exitErr != nil {
			err = c.exitError()
		}
	case <-timer.C:
		err = fmt.Errorf("codex app-server shutdown: %w", ErrTimeout)
		killErr := c.cmd.Process.Kill()
		if killErr != nil && !errors.Is(killErr, os.ErrProcessDone) {
			err = errors.Join(err, killErr)
		}
		timer.Reset(shutdownTimeout)
		select {
		case <-c.exited:
		case <-timer.C:
			err = errors.Join(err, errors.New("codex app-server did not reap after kill"))
		}
	}
	_ = c.stdout.Close()
	return err
}
