package proc

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"
)

// Spawner starts long-lived subprocesses whose stdio is streamed rather
// than collected, e.g. an MCP server driven over stdin/stdout.
type Spawner interface {
	// Spawn starts cmd with stdout and stderr copied into the given
	// writers. cmd.Stdin is ignored; write to Process.Stdin instead.
	Spawn(ctx context.Context, cmd Cmd, stdout, stderr io.Writer) (*Process, error)
}

// Process is a running subprocess started by a Spawner.
type Process struct {
	// Stdin is the write end of the subprocess's standard input. Closing
	// it delivers EOF to the subprocess.
	Stdin io.WriteCloser

	done     chan struct{}
	mu       sync.Mutex
	exitCode int
	waitErr  error
	kill     func() error
}

// NewProcess builds a Process from its parts. Spawner implementations
// (including test fakes) call Exit exactly once when the process ends.
func NewProcess(stdin io.WriteCloser, kill func() error) *Process {
	return &Process{Stdin: stdin, done: make(chan struct{}), exitCode: -1, kill: kill}
}

// Exit records the process's exit status and releases Done waiters.
func (p *Process) Exit(code int, err error) {
	p.mu.Lock()
	p.exitCode = code
	p.waitErr = err
	p.mu.Unlock()
	close(p.done)
}

// Done is closed once the process has exited and its output has been
// fully copied to the writers passed to Spawn.
func (p *Process) Done() <-chan struct{} { return p.done }

// ExitCode returns the exit status, or -1 if the process has not exited
// or was killed by a signal.
func (p *Process) ExitCode() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.exitCode
}

// Kill terminates the process immediately.
func (p *Process) Kill() error {
	if p.kill == nil {
		return nil
	}
	return p.kill()
}

// Spawn implements Spawner using exec.CommandContext. Canceling ctx kills
// the process.
func (OS) Spawn(ctx context.Context, cmd Cmd, stdout, stderr io.Writer) (*Process, error) {
	// #nosec G204 -- proc.Spawner is the abstraction for running caller-supplied subprocess commands
	c := exec.CommandContext(ctx, cmd.Name, cmd.Args...)
	if cmd.Dir != "" {
		c.Dir = cmd.Dir
	}
	if cmd.Env != nil {
		c.Env = cmd.Env
	}
	c.Stdout = stdout
	c.Stderr = stderr
	// Grandchildren (npx → node, sh -c → server) can hold the output
	// pipes open after the direct child exits; don't let Wait hang on them.
	c.WaitDelay = 2 * time.Second
	stdin, err := c.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	if err := c.Start(); err != nil {
		return nil, err
	}
	p := NewProcess(stdin, func() error { return c.Process.Kill() })
	go func() {
		err := c.Wait()
		p.Exit(c.ProcessState.ExitCode(), err)
	}()
	return p, nil
}
