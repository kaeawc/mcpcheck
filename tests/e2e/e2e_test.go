// Package e2e runs mcpcheck's static and live intakes against the
// reference servers in tests/servers (see tests/servers/README.md).
// Build them first with tests/servers/setup.sh, then:
//
//	MCPCHECK_E2E=1 go test ./tests/e2e/ -count=1 -v
//
// Servers whose built artifacts are missing are skipped, unless
// MCPCHECK_E2E_STRICT=1 (as in CI), which makes them failures.
package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/kaeawc/mcpcheck/internal/mcpmodel"
	"github.com/kaeawc/mcpcheck/internal/proc"
	_ "github.com/kaeawc/mcpcheck/internal/rules"
	"github.com/kaeawc/mcpcheck/internal/scanner"
	v2 "github.com/kaeawc/mcpcheck/internal/v2"
)

type httpEntry struct {
	Argv      []string `json:"argv"`
	URL       string   `json:"url"`
	Transport string   `json:"transport"`
}

type server struct {
	ID       string     `json:"id"`
	Language string     `json:"language"`
	SDK      string     `json:"sdk"`
	Source   string     `json:"source"`
	Stdio    []string   `json:"stdio"`
	HTTP     *httpEntry `json:"http"`
	SSE      *httpEntry `json:"sse"`
}

var repoRoot = filepath.Join("..", "..")

var canonical = map[string]struct {
	description string
	properties  string
	required    string
}{
	"get_weather": {"Get the current weather for a city.", "city,units", "city"},
	"delete_file": {"Delete a file inside the sandbox directory.", "confirm,path", "confirm,path"},
	"fetch_url":   {"Fetch a URL and return the response body.", "url", "url"},
}

func loadManifest(t *testing.T) []server {
	t.Helper()
	if os.Getenv("MCPCHECK_E2E") == "" {
		t.Skip("set MCPCHECK_E2E=1 to run against tests/servers (build them with tests/servers/setup.sh)")
	}
	raw, err := os.ReadFile(filepath.Join(repoRoot, "tests", "servers", "servers.json"))
	if err != nil {
		t.Fatal(err)
	}
	var servers []server
	if err := json.Unmarshal(raw, &servers); err != nil {
		t.Fatal(err)
	}
	return servers
}

func TestReferenceServers(t *testing.T) {
	for _, srv := range loadManifest(t) {
		dir := filepath.Join(repoRoot, "tests", "servers", srv.ID)
		static, err := scanner.Load(filepath.Join(repoRoot, srv.Source))
		if err != nil {
			t.Fatalf("%s: static intake: %v", srv.ID, err)
		}
		t.Run(srv.ID+"/static", func(t *testing.T) { assertCanonical(t, static) })
		if srv.Stdio != nil {
			t.Run(srv.ID+"/stdio", func(t *testing.T) {
				requireArtifact(t, dir, srv.Stdio)
				checkLive(t, static, scanner.LiveOptions{Command: absCommand(dir, srv.Stdio), Dir: dir})
			})
		}
		for _, h := range []*httpEntry{srv.HTTP, srv.SSE} {
			if h == nil {
				continue
			}
			t.Run(srv.ID+"/"+h.Transport, func(t *testing.T) {
				requireArtifact(t, dir, h.Argv)
				url := startHTTPServer(t, dir, h)
				checkLive(t, static, scanner.LiveOptions{URL: url})
			})
		}
	}
}

func checkLive(t *testing.T, static *mcpmodel.ToolSet, opts scanner.LiveOptions) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	live, err := scanner.LoadLive(ctx, opts)
	if err != nil {
		t.Fatalf("live: %v", err)
	}
	s := live.Server
	t.Logf("%s %s %s: %s era, protocol %s, %d tools", s.Name, s.Version, s.Transport, s.Era, s.ProtocolVersion, len(live.Tools))
	assertCanonical(t, live)
	rules := v2.All()
	for _, f := range v2.RunComparison(rules, static, live) {
		t.Errorf("static vs live: [%s] %s %s", f.RuleID, f.ToolName, f.Message)
	}
	for _, f := range analyze(rules, live) {
		if f.Category == v2.CatProtocol {
			t.Logf("protocol finding: %s [%s] %s %s", f.Severity, f.RuleID, f.ToolName, f.Message)
		}
	}
}

func analyze(rules []*v2.Rule, set *mcpmodel.ToolSet) []v2.Finding {
	var findings []v2.Finding
	for i := range set.Tools {
		findings = append(findings, v2.Run(rules, &set.Tools[i])...)
	}
	return append(findings, v2.RunProject(rules, set)...)
}

func assertCanonical(t *testing.T, set *mcpmodel.ToolSet) {
	t.Helper()
	if len(set.Tools) != len(canonical) {
		t.Fatalf("got %d tools, want %d", len(set.Tools), len(canonical))
	}
	for _, tool := range set.Tools {
		want, ok := canonical[tool.Name]
		if !ok {
			t.Errorf("unexpected tool %q", tool.Name)
			continue
		}
		props, required := shape(tool.InputSchema)
		if tool.Description != want.description || props != want.properties || required != want.required {
			t.Errorf("%s: description %q properties %s required %s; want %q %s %s",
				tool.Name, tool.Description, props, required, want.description, want.properties, want.required)
		}
	}
}

func shape(schema map[string]any) (props, required string) {
	var p, r []string
	if m, ok := schema["properties"].(map[string]any); ok {
		for k := range m {
			p = append(p, k)
		}
	}
	if list, ok := schema["required"].([]any); ok {
		for _, v := range list {
			r = append(r, fmt.Sprint(v))
		}
	}
	sort.Strings(p)
	sort.Strings(r)
	return strings.Join(p, ","), strings.Join(r, ",")
}

func requireArtifact(t *testing.T, dir string, argv []string) {
	t.Helper()
	if strings.Contains(argv[0], "/") {
		if _, err := os.Stat(filepath.Join(dir, argv[0])); err != nil {
			if os.Getenv("MCPCHECK_E2E_STRICT") == "1" {
				t.Fatalf("%s not built (run tests/servers/setup.sh)", argv[0])
			}
			t.Skipf("%s not built (run tests/servers/setup.sh)", argv[0])
		}
	}
}

func absCommand(dir string, argv []string) []string {
	out := append([]string(nil), argv...)
	if strings.Contains(out[0], "/") {
		out[0], _ = filepath.Abs(filepath.Join(dir, out[0]))
	}
	return out
}

// startHTTPServer launches an HTTP-mode server on a free port and waits
// until it accepts connections.
func startHTTPServer(t *testing.T, dir string, h *httpEntry) string {
	t.Helper()
	port := freePort(t)
	argv := make([]string, len(h.Argv))
	for i, a := range absCommand(dir, h.Argv) {
		argv[i] = strings.ReplaceAll(a, "{port}", port)
	}
	ctx, cancel := context.WithCancel(context.Background())
	p, err := proc.OS{}.Spawn(ctx, proc.Cmd{Name: argv[0], Args: argv[1:], Dir: dir}, io.Discard, io.Discard)
	if err != nil {
		cancel()
		t.Fatalf("start %v: %v", argv, err)
	}
	t.Cleanup(func() {
		cancel()
		<-p.Done()
	})
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", "127.0.0.1:"+port, 200*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return strings.ReplaceAll(h.URL, "{port}", port)
		}
		select {
		case <-p.Done():
			t.Fatalf("%v exited with status %d before listening", argv, p.ExitCode())
		case <-time.After(100 * time.Millisecond):
		}
	}
	t.Fatalf("%v did not listen on %s", argv, port)
	return ""
}

func freePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return fmt.Sprint(l.Addr().(*net.TCPAddr).Port)
}
