package mcpclient

import (
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/kaeawc/mcpcheck/internal/mcpmodel"
)

// maxNoiseLines caps how many non-protocol stdout lines are kept; one
// misbehaving logger shouldn't balloon memory or the report.
const maxNoiseLines = 20

// Recorder collects protocol observations from transports and the client,
// and optionally writes a wire trace. Safe for concurrent use.
type Recorder struct {
	mu         sync.Mutex
	trace      io.Writer
	noise      []string
	noiseTotal int
	violations []mcpmodel.Violation
	seen       map[string]bool
}

// NewRecorder returns a Recorder. trace may be nil.
func NewRecorder(trace io.Writer) *Recorder {
	return &Recorder{trace: trace, seen: map[string]bool{}}
}

// Violation records a protocol problem. Identical messages are recorded
// once so a server that repeats a mistake on every message yields one
// finding.
func (r *Recorder) Violation(kind mcpmodel.ViolationKind, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	r.mu.Lock()
	defer r.mu.Unlock()
	key := string(kind) + "\x00" + msg
	if r.seen[key] {
		return
	}
	r.seen[key] = true
	r.violations = append(r.violations, mcpmodel.Violation{Kind: kind, Message: msg})
	if r.trace != nil {
		_, _ = fmt.Fprintf(r.trace, "!! %s: %s\n", kind, msg)
	}
}

// Noise records a line of non-protocol output on a message stream.
func (r *Recorder) Noise(line string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.noiseTotal++
	if len(r.noise) < maxNoiseLines {
		r.noise = append(r.noise, line)
	}
	if r.trace != nil {
		_, _ = fmt.Fprintf(r.trace, "?? %s\n", line)
	}
}

// Trace writes one wire event when tracing is enabled. dir is "->" for
// client-to-server and "<-" for server-to-client.
func (r *Recorder) Trace(dir string, raw []byte) {
	if r.trace == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	_, _ = fmt.Fprintf(r.trace, "%s %s\n", dir, strings.TrimSpace(string(raw)))
}

// Tracef writes a free-form trace line when tracing is enabled.
func (r *Recorder) Tracef(format string, args ...any) {
	if r.trace == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	_, _ = fmt.Fprintf(r.trace, "-- "+format+"\n", args...)
}

// apply copies the recorded observations into s.
func (r *Recorder) apply(s *mcpmodel.Server) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s.NonProtocolOutput = append([]string(nil), r.noise...)
	if extra := r.noiseTotal - len(r.noise); extra > 0 {
		s.NonProtocolOutput = append(s.NonProtocolOutput, fmt.Sprintf("… and %d more line(s)", extra))
	}
	s.Violations = append([]mcpmodel.Violation(nil), r.violations...)
}
