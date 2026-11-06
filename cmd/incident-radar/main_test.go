package main_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// binary - путь к собранному бинарнику, заполняется в TestMain
var binary string

const testLine = `93.180.71.3 - - [17/May/2015:08:05:32 +0000] "GET /downloads/product_1 HTTP/1.1" 304 0 "-" "Debian APT-HTTP/1.3"`

// TestMain - один раз собирает бинарник перед тестами пакета
func TestMain(m *testing.M) {
	os.Exit(buildAndRun(m))
}

// buildAndRun - собирает бинарник во временную папку и запускает тесты
func buildAndRun(m *testing.M) int {
	dir, err := os.MkdirTemp("", "incident-radar-e2e")
	if err != nil {
		fmt.Println("cannot create temp dir:", err)
		return 1
	}
	defer func() { _ = os.RemoveAll(dir) }()

	binary = filepath.Join(dir, "incident-radar")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	build := exec.CommandContext(ctx, "go", "build", "-o", binary, ".") //nolint:gosec // собираем свой же бинарник во временную папку
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Printf("build failed: %v\n%s\n", err, out)
		return 1
	}
	return m.Run()
}

// runBinary - запускает бинарник с аргументами и stdin, возвращает exit code, stdout и stderr
func runBinary(t *testing.T, stdin string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var out, errOut strings.Builder
	cmd := exec.CommandContext(ctx, binary, args...) //nolint:gosec // запускаем свой бинарник с аргументами теста
	cmd.Stdin = strings.NewReader(stdin)
	cmd.Stdout = &out
	cmd.Stderr = &errOut

	err := cmd.Run()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), out.String(), errOut.String()
	}
	if err != nil {
		t.Fatalf("cannot run binary: %v", err)
	}
	return 0, out.String(), errOut.String()
}

// writeLog - создает файл лога во временной папке
func writeLog(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "access.log")
	if err := os.WriteFile(path, []byte(testLine+"\n"+testLine+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestE2E_Success - успешный запуск отдельным процессом создает отчет в каждом формате
func TestE2E_Success(t *testing.T) {
	dir := t.TempDir()
	logFile := writeLog(t, dir)

	tests := []struct {
		name   string
		format string
		output string
		want   string
	}{
		{"json", "json", "report.json", `"totalRequestsCount": 2`},
		{"markdown", "markdown", "report.md", "| Количество запросов | 2 |"},
		{"adoc", "adoc", "report.adoc", "|Количество запросов |2"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := filepath.Join(dir, tt.output)

			code, _, stderr := runBinary(t, "", "--path", logFile, "--format", tt.format, "--output", out)
			if code != 0 {
				t.Fatalf("exit code = %d, want 0, stderr: %s", code, stderr)
			}

			data, err := os.ReadFile(out) //nolint:gosec // путь из t.TempDir()
			if err != nil {
				t.Fatalf("report was not created: %v", err)
			}
			if !strings.Contains(string(data), tt.want) {
				t.Fatalf("report has no %q:\n%s", tt.want, data)
			}
			if tt.format == "json" && !json.Valid(data) {
				t.Fatalf("invalid JSON:\n%s", data)
			}
		})
	}
}

// TestE2E_UsageError - ошибки использования дают настоящий exit code 2, существующий отчет не меняется
func TestE2E_UsageError(t *testing.T) {
	dir := t.TempDir()
	logFile := writeLog(t, dir)

	existing := filepath.Join(dir, "existing.md")
	if err := os.WriteFile(existing, []byte("old report"), 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		args []string
	}{
		{"no path", []string{"--output", filepath.Join(dir, "a.md")}},
		{"existing output", []string{"--path", logFile, "--output", existing}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, _, stderr := runBinary(t, "", tt.args...)
			if code != 2 {
				t.Fatalf("exit code = %d, want 2, stderr: %s", code, stderr)
			}
		})
	}

	got, err := os.ReadFile(existing) //nolint:gosec // путь из t.TempDir()
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "old report" {
		t.Fatalf("existing output was changed: %q", got)
	}
}

// TestE2E_StdinPipe - лог из pipe: --path - читает stdin, отчет идет в stdout, сводка WARN в stderr
func TestE2E_StdinPipe(t *testing.T) {
	code, stdout, stderr := runBinary(t, testLine+"\nbroken\n"+testLine+"\n", "--path", "-", "-f", "json", "--quiet", "--bucket", "1m")
	if code != 0 {
		t.Fatalf("exit code = %d, stderr: %s", code, stderr)
	}
	if stderr != "WARN: skipped 1 malformed log lines\n" {
		t.Fatalf("stderr = %q", stderr)
	}

	var rep struct {
		SchemaVersion      string `json:"schemaVersion"`
		TotalRequestsCount int    `json:"totalRequestsCount"`
		Timeline           struct {
			Points []struct {
				Requests int `json:"requests"`
			} `json:"points"`
		} `json:"timeline"`
	}
	if err := json.Unmarshal([]byte(stdout), &rep); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout)
	}
	if rep.SchemaVersion == "" || rep.TotalRequestsCount != 2 || len(rep.Timeline.Points) != 1 || rep.Timeline.Points[0].Requests != 2 {
		t.Fatalf("report = %+v", rep)
	}
}
