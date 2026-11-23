// Command server is an MCP fixture server built on
// github.com/mark3labs/mcp-go. It exposes the three canonical mcpcheck
// corpus tools over stdio, streamable HTTP, or legacy SSE.
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

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const usage = "usage: server stdio | http --port N | sse --port N"

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
		return server.ServeStdio(newServer())
	case "http":
		return serveHTTP(args[1:], "/mcp", func(s *server.MCPServer) http.Handler {
			return server.NewStreamableHTTPServer(s)
		})
	case "sse":
		// SSEServer routes both GET /sse and POST /message itself.
		return serveHTTP(args[1:], "/", func(s *server.MCPServer) http.Handler {
			return server.NewSSEServer(s)
		})
	default:
		return errors.New(usage)
	}
}

func newServer() *server.MCPServer {
	s := server.NewMCPServer("mcpcheck-go-mcpgo", "0.1.0", server.WithToolCapabilities(false))

	s.AddTool(mcp.NewTool("get_weather",
		mcp.WithDescription("Get the current weather for a city."),
		mcp.WithString("city", mcp.Required(), mcp.Description("City name")),
		mcp.WithString("units", mcp.Enum("celsius", "fahrenheit")),
		mcp.WithReadOnlyHintAnnotation(true),
	), getWeather)

	s.AddTool(mcp.NewTool("delete_file",
		mcp.WithDescription("Delete a file inside the sandbox directory."),
		mcp.WithString("path", mcp.Required()),
		mcp.WithBoolean("confirm", mcp.Required()),
		mcp.WithDestructiveHintAnnotation(true),
	), deleteFile)

	s.AddTool(mcp.NewTool("fetch_url",
		mcp.WithDescription("Fetch a URL and return the response body."),
		mcp.WithString("url", mcp.Required()),
		mcp.WithOpenWorldHintAnnotation(true),
	), fetchURL)

	return s
}

func getWeather(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	city, err := req.RequireString("city")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText("Sunny in " + city), nil
}

func deleteFile(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	path, err := req.RequireString("path")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText("would delete " + path), nil
}

func fetchURL(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	u, err := req.RequireString("url")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText("would fetch " + u), nil
}

func serveHTTP(args []string, path string, handler func(*server.MCPServer) http.Handler) error {
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
