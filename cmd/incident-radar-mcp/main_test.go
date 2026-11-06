package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// binary - путь к собранному бинарнику, заполняется в TestMain
var binary string

const testLine = `93.180.71.3 - - [17/May/2015:08:05:32 +0000] "GET /downloads/product_1 HTTP/1.1" 500 10 "-" "Debian APT-HTTP/1.3"`

// TestMain - один раз собирает бинарник перед тестами пакета
func TestMain(m *testing.M) {
	os.Exit(buildAndRun(m))
}

// buildAndRun - собирает бинарник во временную папку и запускает тесты
func buildAndRun(m *testing.M) int {
	dir, err := os.MkdirTemp("", "incident-radar-mcp-e2e")
	if err != nil {
		fmt.Println("cannot create temp dir:", err)
		return 1
	}
	defer func() { _ = os.RemoveAll(dir) }()

	binary = filepath.Join(dir, "incident-radar-mcp")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	build := exec.CommandContext(ctx, "go", "build", "-o", binary, ".") //nolint:gosec // собираем свой же бинарник во временную папку
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Printf("build failed: %v\n%s\n", err, out)
		return 1
	}
	return m.Run()
}

// TestE2E_Stdio - клиент SDK запускает бинарник как подпроцесс и вызывает analyze_logs по stdio
func TestE2E_Stdio(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "access.log"), []byte(testLine+"\n"+testLine+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, binary, "--root", root) //nolint:gosec // запускаем свой бинарник
	cmd.Stderr = &stderr

	client := mcp.NewClient(&mcp.Implementation{Name: "e2e", Version: "test"}, nil)
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatalf("connect: %v, stderr: %s", err, stderr.String())
	}

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "analyze_logs",
		Arguments: map[string]any{"path": "access.log", "bucket": "1h"},
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var rep struct {
		TotalRequestsCount int     `json:"totalRequestsCount"`
		ErrorRate          float64 `json:"errorRate"`
	}
	if err := json.Unmarshal(data, &rep); err != nil {
		t.Fatal(err)
	}
	if res.IsError || rep.TotalRequestsCount != 2 || rep.ErrorRate != 1 {
		t.Fatalf("result = %s", data)
	}

	if err := session.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if !strings.Contains(stderr.String(), `"msg":"tool call"`) || !strings.Contains(stderr.String(), `"tool":"analyze_logs"`) {
		t.Fatalf("no structured tool call log in stderr:\n%s", stderr.String())
	}
}

// TestParseFlags - корректные флаги разбираются, неверные возвращают ошибку
func TestParseFlags(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "file.log")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := parseFlags([]string{
		"--transport", "http", "--root", root, "--allow-dir", root, "--allow-url", "https://logs.example/nginx/",
		"--allow-url", "http://127.0.0.1:8000/", "--log-level", "debug", "--log-format", "text",
	}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.transport != "http" || cfg.root != root || len(cfg.allowURLs) != 2 || cfg.allowURLs.String() == "" {
		t.Fatalf("cfg = %+v", cfg)
	}

	tests := []struct {
		name string
		args []string
	}{
		{"bad transport", []string{"--transport", "sse"}},
		{"missing root", []string{"--root", filepath.Join(root, "nope")}},
		{"root is a file", []string{"--root", file}},
		{"bad allow-dir", []string{"--allow-dir", filepath.Join(root, "nope")}},
		{"bad allow-url", []string{"--allow-url", "ftp://logs"}},
		{"bad log level", []string{"--log-level", "loud"}},
		{"bad log format", []string{"--log-format", "xml"}},
		{"zero timeout", []string{"--call-timeout", "0s"}},
		{"extra args", []string{"extra"}},
		{"unknown flag", []string{"--nope"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := parseFlags(tt.args, &bytes.Buffer{}); err == nil {
				t.Fatalf("parseFlags(%v) = nil error", tt.args)
			}
		})
	}
}

// TestRun_Exits - неверные флаги дают код 2, --help - 0, занятый адрес HTTP - 1
func TestRun_Exits(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want int
	}{
		{"usage error", []string{"--transport", "sse"}, 2},
		{"help", []string{"--help"}, 0},
		{"bad address", []string{"--transport", "http", "--addr", "256.0.0.1:1"}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := run(context.Background(), tt.args, "", &bytes.Buffer{}); got != tt.want {
				t.Fatalf("run(%v) = %d, want %d", tt.args, got, tt.want)
			}
		})
	}
}

// TestRun_HTTPShutdown - HTTP-сервер останавливается по отмене context с кодом 0
func TestRun_HTTPShutdown(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	var stderr bytes.Buffer
	if got := run(ctx, []string{"--transport", "http", "--addr", "127.0.0.1:0", "--root", t.TempDir()}, "", &stderr); got != 0 {
		t.Fatalf("run = %d, stderr: %s", got, stderr.String())
	}
	if !strings.Contains(stderr.String(), "HTTP server stopped") {
		t.Fatalf("no shutdown log:\n%s", stderr.String())
	}
}

// TestIsLoopback - распознавание локальных адресов для предупреждения о токене
func TestIsLoopback(t *testing.T) {
	tests := map[string]bool{
		"127.0.0.1:8765": true,
		"localhost:80":   true,
		"[::1]:80":       true,
		"0.0.0.0:8765":   false,
		":8765":          false,
		"10.0.0.5:80":    false,
		"bad":            false,
	}
	for addr, want := range tests {
		if got := isLoopback(addr); got != want {
			t.Fatalf("isLoopback(%q) = %v, want %v", addr, got, want)
		}
	}
}
