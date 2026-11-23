package mcpclient

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kaeawc/mcpcheck/internal/mcpmodel"
	"github.com/kaeawc/mcpcheck/internal/proc"
)

// Transport moves JSON-RPC messages between the client and one server.
type Transport interface {
	// Send delivers one message to the server.
	Send(ctx context.Context, m Message) error
	// Recv returns the next message from the server, in arrival order.
	Recv(ctx context.Context) (Message, error)
	// Close ends the session and releases resources.
	Close() error
	// Name is the transport kind recorded on mcpmodel.Server.Transport.
	Name() string
}

// diagnoser is implemented by transports that can explain a failure
// beyond the error itself, e.g. a subprocess's exit status and stderr.
type diagnoser interface {
	Diagnose() string
}

// versionAware is implemented by transports that must echo the
// negotiated protocol version (Streamable HTTP's MCP-Protocol-Version).
type versionAware interface {
	SetProtocolVersion(v string)
}

// ErrServerClosed reports that the server ended the message stream.
var ErrServerClosed = errors.New("server closed the connection")

// Stdio is the newline-delimited JSON-RPC transport over a pair of
// streams. Lines that aren't JSON are recorded as noise and skipped
// rather than failing the session.
type Stdio struct {
	w   io.Writer
	rec *Recorder
	in  *inbox
	wmu sync.Mutex
}

// NewStdio starts reading server messages from r and returns a transport
// that writes client messages to w.
func NewStdio(w io.Writer, r io.Reader, rec *Recorder) *Stdio {
	t := &Stdio{w: w, rec: rec, in: newInbox()}
	go t.readLoop(r)
	return t
}

// Name implements Transport.
func (t *Stdio) Name() string { return "stdio" }

// Send implements Transport.
func (t *Stdio) Send(_ context.Context, m Message) error {
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	t.rec.Trace("->", raw)
	t.wmu.Lock()
	defer t.wmu.Unlock()
	if _, err := t.w.Write(append(raw, '\n')); err != nil {
		return fmt.Errorf("write to server stdin: %w", err)
	}
	return nil
}

// Recv implements Transport.
func (t *Stdio) Recv(ctx context.Context) (Message, error) { return t.in.recv(ctx) }

// Close implements Transport. The streams are owned by the caller.
func (t *Stdio) Close() error { return nil }

func (t *Stdio) readLoop(r io.Reader) {
	br := bufio.NewReader(r)
	for {
		line, err := br.ReadBytes('\n')
		if trimmed := bytes.TrimSpace(line); len(trimmed) > 0 {
			t.handleLine(br, trimmed)
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				err = ErrServerClosed
			}
			t.in.close(err)
			return
		}
	}
}

func (t *Stdio) handleLine(br *bufio.Reader, line []byte) {
	if line[0] == '{' || line[0] == '[' {
		t.handleJSON(line)
		return
	}
	if n, ok := contentLength(line); ok {
		t.rec.Violation(mcpmodel.ViolationTransport,
			"server frames stdio messages with LSP-style Content-Length headers; MCP stdio messages are newline-delimited JSON with no headers")
		if body, err := readFramedBody(br, n); err == nil {
			t.handleJSON(bytes.TrimSpace(body))
		}
		return
	}
	t.rec.Noise(string(line))
}

func (t *Stdio) handleJSON(line []byte) {
	t.rec.Trace("<-", line)
	msgs, err := decodeMessages(line)
	if err != nil {
		t.rec.Noise(string(line))
		t.rec.Violation(mcpmodel.ViolationJSONRPC, "server wrote a line that is not valid JSON-RPC: %v", err)
		return
	}
	if len(msgs) > 1 {
		t.rec.Violation(mcpmodel.ViolationJSONRPC,
			"server sent a JSON-RPC batch; batching was removed in protocol 2025-06-18 and many clients reject it")
	}
	for _, m := range msgs {
		t.in.push(m)
	}
}

// contentLength parses an LSP-style "Content-Length: N" header line.
func contentLength(line []byte) (int, bool) {
	const prefix = "content-length:"
	if len(line) <= len(prefix) || !strings.EqualFold(string(line[:len(prefix)]), prefix) {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(line[len(prefix):])))
	if err != nil || n < 0 || n > maxFramedBody {
		return 0, false
	}
	return n, true
}

// maxFramedBody bounds a Content-Length-framed message.
const maxFramedBody = 64 << 20

// readFramedBody skips the remaining header lines up to the blank
// separator, then reads exactly n body bytes.
func readFramedBody(br *bufio.Reader, n int) ([]byte, error) {
	for {
		h, err := br.ReadBytes('\n')
		if err != nil {
			return nil, err
		}
		if len(bytes.TrimSpace(h)) == 0 {
			break
		}
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(br, body); err != nil {
		return nil, err
	}
	return body, nil
}

// stderrTailBytes is how much of a server's stderr is kept for error
// reports.
const stderrTailBytes = 4096

// tailBuffer keeps the last n bytes written to it and optionally tees
// everything to another writer.
type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
	n   int
	tee io.Writer
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	b.buf = append(b.buf, p...)
	if len(b.buf) > b.n {
		b.buf = b.buf[len(b.buf)-b.n:]
	}
	b.mu.Unlock()
	if b.tee != nil {
		_, _ = b.tee.Write(p)
	}
	return len(p), nil
}

func (b *tailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}

// StdioProcess is a Stdio transport bound to a spawned server process.
type StdioProcess struct {
	*Stdio
	proc    *proc.Process
	stdout  *io.PipeWriter
	stderr  *tailBuffer
	command string
}

// SpawnStdio starts cmd via spawner and returns a transport speaking to
// it. Server stderr is retained for diagnostics and also copied to
// stderrTee when non-nil.
func SpawnStdio(ctx context.Context, spawner proc.Spawner, cmd proc.Cmd, rec *Recorder, stderrTee io.Writer) (*StdioProcess, error) {
	pr, pw := io.Pipe()
	tail := &tailBuffer{n: stderrTailBytes, tee: stderrTee}
	p, err := spawner.Spawn(ctx, cmd, pw, tail)
	if err != nil {
		_ = pw.Close()
		return nil, fmt.Errorf("start %s: %w", cmd.Name, err)
	}
	t := &StdioProcess{
		Stdio:   NewStdio(p.Stdin, pr, rec),
		proc:    p,
		stdout:  pw,
		stderr:  tail,
		command: strings.Join(append([]string{cmd.Name}, cmd.Args...), " "),
	}
	go func() {
		<-p.Done()
		_ = pw.Close()
	}()
	return t, nil
}

// Close closes the server's stdin, which MCP servers treat as shutdown,
// then kills the process if it hasn't exited shortly after.
func (t *StdioProcess) Close() error {
	_ = t.proc.Stdin.Close()
	select {
	case <-t.proc.Done():
	case <-time.After(2 * time.Second):
		_ = t.proc.Kill()
		<-t.proc.Done()
	}
	return nil
}

// Diagnose describes the process state and recent stderr for errors.
func (t *StdioProcess) Diagnose() string {
	var b strings.Builder
	select {
	case <-t.proc.Done():
		if code := t.proc.ExitCode(); code >= 0 {
			fmt.Fprintf(&b, "server process exited with status %d", code)
		} else {
			b.WriteString("server process was killed")
		}
	default:
		b.WriteString("server process is still running")
	}
	if tail := strings.TrimSpace(t.stderr.String()); tail != "" {
		b.WriteString("; last stderr output:\n")
		for _, line := range strings.Split(tail, "\n") {
			b.WriteString("    " + line + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}
