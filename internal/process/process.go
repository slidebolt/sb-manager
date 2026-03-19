// Package process manages the lifecycle of a single binary's start command.
package process

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"sync"
	"time"

	contract "github.com/slidebolt/sb-contract"
)

// State represents the current state of a managed process.
type State int

const (
	StateStopped State = iota
	StateStarting
	StateRunning
	StateStopping
	StateFailed
)

func (s State) String() string {
	switch s {
	case StateStopped:
		return "stopped"
	case StateStarting:
		return "starting"
	case StateRunning:
		return "running"
	case StateStopping:
		return "stopping"
	case StateFailed:
		return "failed"
	default:
		return "unknown"
	}
}

// Process manages a single binary's start lifecycle.
type Process struct {
	ID   string
	Path string

	mu    sync.Mutex
	state State
	cmd   *exec.Cmd
	stdin io.WriteCloser

	// Ready is closed when the binary sends {"type":"ready"}.
	Ready chan struct{}
	// Payload is the ready payload advertised by this binary.
	Payload json.RawMessage
	// Done is closed when the process exits.
	Done chan struct{}
	// Err holds the exit error, if any.
	Err error

	// OnMessage is called for each runtime message from the binary.
	OnMessage func(contract.RuntimeMessage)
}

// New creates a new Process (not yet started).
func New(id, path string) *Process {
	return &Process{
		ID:    id,
		Path:  path,
		state: StateStopped,
		Ready: make(chan struct{}),
		Done:  make(chan struct{}),
	}
}

// State returns the current process state.
func (p *Process) State() State {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.state
}

// Start launches the binary's start command.
func (p *Process) Start(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.state != StateStopped && p.state != StateFailed {
		return fmt.Errorf("process %s: cannot start in state %s", p.ID, p.state)
	}

	p.state = StateStarting
	p.Ready = make(chan struct{})
	p.Done = make(chan struct{})
	p.Err = nil

	cmd := exec.CommandContext(ctx, p.Path, "start")

	stdin, err := cmd.StdinPipe()
	if err != nil {
		p.state = StateFailed
		return fmt.Errorf("process %s: stdin pipe: %w", p.ID, err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		p.state = StateFailed
		return fmt.Errorf("process %s: stdout pipe: %w", p.ID, err)
	}

	cmd.Stderr = nil // stderr handled below

	stderr, err := cmd.StderrPipe()
	if err != nil {
		p.state = StateFailed
		return fmt.Errorf("process %s: stderr pipe: %w", p.ID, err)
	}

	if err := cmd.Start(); err != nil {
		p.state = StateFailed
		return fmt.Errorf("process %s: start: %w", p.ID, err)
	}

	p.cmd = cmd
	p.stdin = stdin

	// Read stdout (control protocol) in background.
	go p.readOutput(stdout)

	// Forward stderr (application logs) in background.
	go p.forwardStderr(stderr)

	// Wait for exit in background.
	go p.waitExit()

	return nil
}

// SendDependency sends a dependency payload to the binary over stdin.
func (p *Process) SendDependency(id string, payload json.RawMessage) error {
	msg := contract.ControlMessage{
		Type:    contract.ControlDependency,
		ID:      id,
		Payload: payload,
	}
	return contract.WriteJSON(p.stdin, msg)
}

// Shutdown sends the shutdown control message and waits for exit.
func (p *Process) Shutdown(timeout time.Duration) error {
	p.mu.Lock()
	if p.state != StateRunning && p.state != StateStarting {
		p.mu.Unlock()
		return nil
	}
	p.state = StateStopping
	stdin := p.stdin
	cmd := p.cmd
	p.mu.Unlock()

	// Send shutdown message.
	msg := contract.ControlMessage{Type: contract.ControlShutdown}
	if err := contract.WriteJSON(stdin, msg); err != nil {
		slog.Warn("failed to send shutdown", "binary", p.ID, "error", err)
	}
	stdin.Close()

	// Wait for graceful exit or kill.
	select {
	case <-p.Done:
		return p.Err
	case <-time.After(timeout):
		slog.Warn("shutdown timeout, killing", "binary", p.ID)
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
		<-p.Done
		return fmt.Errorf("process %s: killed after timeout", p.ID)
	}
}

func (p *Process) readOutput(r io.Reader) {
	scanner := bufio.NewScanner(r)
	readyOnce := sync.Once{}

	for scanner.Scan() {
		var msg contract.RuntimeMessage
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			slog.Warn("bad stdout line", "binary", p.ID, "line", scanner.Text())
			continue
		}

		switch msg.Type {
		case contract.RuntimeReady:
			readyOnce.Do(func() {
				p.mu.Lock()
				p.Payload = msg.Payload
				p.state = StateRunning
				p.mu.Unlock()
				close(p.Ready)
			})
		case contract.RuntimeError:
			slog.Error(msg.Message, "binary", p.ID)
		case contract.RuntimeLog:
			slog.Info(msg.Message, "binary", p.ID, "level", msg.Level)
		}

		if p.OnMessage != nil {
			p.OnMessage(msg)
		}
	}
}

func (p *Process) waitExit() {
	err := p.cmd.Wait()

	p.mu.Lock()
	if p.state != StateStopping {
		p.state = StateFailed
	} else {
		p.state = StateStopped
	}
	p.Err = err
	p.mu.Unlock()

	close(p.Done)
}

// forwardStderr reads the child's stderr line by line and re-emits each
// line to the manager's stderr. If the child writes structured JSON (slog),
// the output passes through unchanged — Dozzle and Docker see it as-is.
func (p *Process) forwardStderr(r io.Reader) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		// Write directly to os.Stderr so the line passes through verbatim.
		// The child's slog JSON already has "binary":"<id>" in it.
		fmt.Fprintln(os.Stderr, scanner.Text())
	}
}
