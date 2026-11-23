// Command server is an MCP fixture server built on the official
// github.com/modelcontextprotocol/go-sdk. It exposes the three canonical
// mcpcheck corpus tools over stdio, streamable HTTP, or legacy SSE.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const usage = "usage: server stdio | http --port N | sse --port N"

type weatherInput struct {
	City  string `json:"city" jsonschema:"City name"`
	Units string `json:"units,omitempty"`
}

type deleteFileInput struct {
	Path    string `json:"path"`
	Confirm bool   `json:"confirm"`
}

type fetchURLInput struct {
	URL string `json:"url"`
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "server:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New(usage)
	}
	switch args[0] {
	case "stdio":
		return newServer().Run(context.Background(), &mcp.StdioTransport{})
	case "http":
		return serveHTTP(args[1:], "/mcp", func(s *mcp.Server) http.Handler {
			return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s }, nil)
		})
	case "sse":
		return serveHTTP(args[1:], "/sse", func(s *mcp.Server) http.Handler {
			return mcp.NewSSEHandler(func(*http.Request) *mcp.Server { return s }, nil)
		})
	default:
		return errors.New(usage)
	}
}

func newServer() *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "mcpcheck-go-sdk", Version: "0.1.0"}, nil)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_weather",
		Description: "Get the current weather for a city.",
		InputSchema: weatherSchema(),
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, getWeather)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "delete_file",
		Description: "Delete a file inside the sandbox directory.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(true)},
	}, deleteFile)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "fetch_url",
		Description: "Fetch a URL and return the response body.",
		Annotations: &mcp.ToolAnnotations{OpenWorldHint: boolPtr(true)},
	}, fetchURL)

	return server
}

// weatherSchema post-processes the inferred schema because jsonschema struct
// tags in this SDK can only carry a description, not an enum.
func weatherSchema() *jsonschema.Schema {
	s, err := jsonschema.For[weatherInput](nil)
	if err != nil {
		panic(fmt.Sprintf("infer get_weather schema: %v", err))
	}
	s.Properties["units"].Enum = []any{"celsius", "fahrenheit"}
	return s
}

func getWeather(_ context.Context, _ *mcp.CallToolRequest, in weatherInput) (*mcp.CallToolResult, any, error) {
	return textResult("Sunny in " + in.City), nil, nil
}

func deleteFile(_ context.Context, _ *mcp.CallToolRequest, in deleteFileInput) (*mcp.CallToolResult, any, error) {
	return textResult("would delete " + in.Path), nil, nil
}

func fetchURL(_ context.Context, _ *mcp.CallToolRequest, in fetchURLInput) (*mcp.CallToolResult, any, error) {
	return textResult("would fetch " + in.URL), nil, nil
}

func textResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}
}

func boolPtr(b bool) *bool { return &b }

func serveHTTP(args []string, path string, handler func(*mcp.Server) http.Handler) error {
	port, err := parsePort(args)
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	mux.Handle(path, handler(newServer()))
	srv := &http.Server{
		Addr:              "127.0.0.1:" + strconv.Itoa(port),
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	return srv.ListenAndServe()
}

func parsePort(args []string) (int, error) {
	fs := flag.NewFlagSet("server", flag.ContinueOnError)
	port := fs.Int("port", 0, "TCP port to listen on (127.0.0.1)")
	if err := fs.Parse(args); err != nil {
		return 0, fmt.Errorf("parse flags: %w", err)
	}
	if *port <= 0 || *port > 65535 {
		return 0, fmt.Errorf("--port is required (1-65535): %s", usage)
	}
	return *port, nil
}
