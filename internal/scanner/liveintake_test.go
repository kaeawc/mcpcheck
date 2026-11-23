package scanner

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// TestMain lets the test binary double as a stdio MCP server, so LoadLive
// is exercised against a real subprocess without any other toolchain.
func TestMain(m *testing.M) {
	switch os.Getenv("MCPCHECK_TEST_SERVER") {
	case "":
		os.Exit(m.Run())
	case "ok":
		serveStdio()
		os.Exit(0)
	case "crash":
		fmt.Fprintln(os.Stderr, "Traceback: ImportError: no module named mcp")
		os.Exit(3)
	}
}

func serveStdio() {
	fmt.Fprintln(os.Stderr, "server log line on stderr is fine")
	in := bufio.NewScanner(os.Stdin)
	for in.Scan() {
		var m struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if json.Unmarshal(in.Bytes(), &m) != nil || len(m.ID) == 0 {
			continue
		}
		var result string
		switch m.Method {
		case "initialize":
			result = `{"protocolVersion":"2025-03-26","capabilities":{"tools":{}},"serverInfo":{"name":"helper","version":"0.0.1"}}`
		case "tools/list":
			result = `{"tools":[{"name":"get_weather","description":"Get the current weather for a city.","inputSchema":{"type":"object"}}]}`
		default:
			continue
		}
		fmt.Printf(`{"jsonrpc":"2.0","id":%s,"result":%s}`+"\n", m.ID, result)
	}
}

func helperCommand(t *testing.T, mode string) LiveOptions {
	t.Helper()
	t.Setenv("MCPCHECK_TEST_SERVER", mode)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return LiveOptions{Command: []string{exe, "-test.run=^$"}}
}

func TestLoadLiveStdioSubprocess(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	set, err := LoadLive(ctx, helperCommand(t, "ok"))
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Tools) != 1 || set.Tools[0].Name != "get_weather" {
		t.Fatalf("tools = %+v", set.Tools)
	}
	s := set.Server
	if s.Transport != "stdio" || s.Name != "helper" || s.ProtocolVersion != "2025-03-26" || len(s.Violations) != 0 {
		t.Errorf("server = %+v", s)
	}
}

func TestLoadLiveReportsCrash(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := LoadLive(ctx, helperCommand(t, "crash"))
	if err == nil {
		t.Fatal("expected error")
	}
	for _, want := range []string{"initialize:", "exited with status 3", "ImportError: no module named mcp"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q:\n%v", want, err)
		}
	}
}

func TestLoadLiveMissingBinary(t *testing.T) {
	_, err := LoadLive(context.Background(), LiveOptions{Command: []string{"definitely-not-an-mcp-server-binary"}})
	if err == nil || !strings.Contains(err.Error(), "start definitely-not-an-mcp-server-binary") {
		t.Fatalf("err = %v", err)
	}
}

func TestLoadLiveRequiresExactlyOneTarget(t *testing.T) {
	if _, err := LoadLive(context.Background(), LiveOptions{}); err == nil {
		t.Error("expected error with no target")
	}
	if _, err := LoadLive(context.Background(), LiveOptions{Command: []string{"x"}, URL: "http://x"}); err == nil {
		t.Error("expected error with two targets")
	}
}
