package claudenative

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"
)

// process owns one Claude Code child: stdin for stream-json input, an owned
// stdout pipe read line by line, and the process tree that cleanup kills.
type process struct {
	cmd      *exec.Cmd
	tree     *processTree
	stdin    io.WriteCloser
	stdout   *os.File
	lines    chan []byte
	readErr  error // read only after lines closes
	exited   chan struct{}
	stopRead chan struct{}
	stopOnce sync.Once
}

func startProcess(path string, args []string, dir string, env []string) (*process, error) {
	cmd := exec.Command(path, args...)
	cmd.Dir, cmd.Env = dir, env // Stderr stays nil: the null device, never retained
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	// Own stdout rather than StdoutPipe so Wait cannot close it before the
	// final line is read. The child receives only the write end.
	stdout, output, err := os.Pipe()
	if err != nil {
		return nil, errors.Join(err, stdin.Close())
	}
	cmd.Stdout = output
	tree, err := startTree(cmd)
	closeErr := output.Close()
	if err != nil {
		return nil, errors.Join(err, stdin.Close(), stdout.Close())
	}
	p := &process{cmd: cmd, tree: tree, stdin: stdin, stdout: stdout, lines: make(chan []byte), exited: make(chan struct{}), stopRead: make(chan struct{})}
	go func() {
		_ = cmd.Wait()
		close(p.exited)
	}()
	if closeErr != nil {
		_ = tree.kill()
		<-p.exited
		tree.close()
		p.stop()
		return nil, closeErr
	}
	go p.read()
	return p, nil
}

func (p *process) read() {
	defer close(p.lines)
	scanner := bufio.NewScanner(p.stdout)
	scanner.Buffer(make([]byte, 64<<10), maxLineBytes)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		select {
		case p.lines <- bytes.Clone(line):
		case <-p.stopRead:
			return
		}
	}
	p.readErr = scanner.Err()
}

// closedError explains why stdout ended before the turn's result.
func (p *process) closedError() error {
	if errors.Is(p.readErr, bufio.ErrTooLong) {
		return fmt.Errorf("%w: stream line exceeds %d bytes", ErrMalformedMessage, maxLineBytes)
	}
	return errors.Join(ErrProcessExit, p.readErr)
}

func (p *process) send(value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if _, err := p.stdin.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("%w: write stream-json input: %w", ErrProcessExit, err)
	}
	return nil
}

func (p *process) running() bool {
	select {
	case <-p.exited:
		return false
	default:
		return true
	}
}

func (p *process) wait(d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-p.exited:
		return true
	case <-timer.C:
		return false
	}
}

func (p *process) stop() {
	p.stopOnce.Do(func() {
		close(p.stopRead)
		_ = p.stdout.Close()
	})
}
