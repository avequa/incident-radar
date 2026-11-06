package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testLine = `93.180.71.3 - - [17/May/2015:08:05:32 +0000] "GET /downloads/product_1 HTTP/1.1" 304 0 "-" "Debian APT-HTTP/1.3"`

// writeLog - создает лог во временной папке и возвращает путь к нему
func writeLog(t *testing.T, dir, content string) string {
	t.Helper()
	path := filepath.Join(dir, "access.log")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// runCLI - запускает Run с буферами вместо потоков процесса
func runCLI(args []string, stdin string) (code int, stdout, stderr string) {
	var out, errOut bytes.Buffer
	code = Run(context.Background(), args, Streams{Stdin: strings.NewReader(stdin), Stdout: &out, Stderr: &errOut})
	return code, out.String(), errOut.String()
}

// TestRun_ExitCodes - успешный запуск дает 0, ошибки ввода и источника - 2
func TestRun_ExitCodes(t *testing.T) {
	dir := t.TempDir()
	logFile := writeLog(t, dir, testLine+"\n"+testLine+"\n")
	out := filepath.Join(dir, "report.md")

	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()

	tests := []struct {
		name string
		args []string
		want int
	}{
		{"success", []string{"--path", logFile, "-o", filepath.Join(dir, "success.md")}, ExitOK},
		{"no path", []string{"-o", out}, ExitUsage},
		{"format xml", []string{"--path", logFile, "-f", "xml", "-o", out}, ExitUsage},
		{"from >= to", []string{"--path", logFile, "-o", out, "--from", "2025-03-05", "--to", "2025-03-01"}, ExitUsage},
		{"missing file", []string{"--path", filepath.Join(dir, "nope.log"), "-o", out}, ExitUsage},
		{"empty glob", []string{"--path", filepath.Join(dir, "*.txt"), "-o", out}, ExitUsage},
		{"http 404", []string{"--path", server.URL + "/access.txt", "-o", out}, ExitUsage},
		{"bucket too small", []string{"--path", logFile, "--bucket", "10ms"}, ExitUsage},
		{"top too large", []string{"--path", logFile, "--top", "100000"}, ExitUsage},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, _, stderr := runCLI(tt.args, "")
			if code != tt.want {
				t.Fatalf("exit code = %d, want %d, stderr: %s", code, tt.want, stderr)
			}
		})
	}
}

// TestRun_InternalError - ошибка записи отчета и паника дают exit code 1
func TestRun_InternalError(t *testing.T) {
	dir := t.TempDir()
	logFile := writeLog(t, dir, testLine+"\n")
	args := []string{"--path", logFile, "-o", filepath.Join(dir, "report.md")}

	tests := []struct {
		name        string
		writeOutput writeFunc
	}{
		{"write error", func(string, []byte) error { return errors.New("disk is full") }},
		{"panic", func(string, []byte) error { panic("unexpected") }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stderr bytes.Buffer
			streams := Streams{Stdin: strings.NewReader(""), Stdout: &bytes.Buffer{}, Stderr: &stderr}
			if code := run(context.Background(), args, streams, tt.writeOutput); code != ExitInternal {
				t.Fatalf("exit code = %d, want %d, stderr: %s", code, ExitInternal, stderr.String())
			}
		})
	}
}

// TestRun_Canceled - отмененный context дает exit code 1 и сообщение interrupted
func TestRun_Canceled(t *testing.T) {
	logFile := writeLog(t, t.TempDir(), testLine+"\n")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var stderr bytes.Buffer
	code := Run(ctx, []string{"--path", logFile}, Streams{Stdout: &bytes.Buffer{}, Stderr: &stderr})
	if code != ExitInternal || !strings.Contains(stderr.String(), "interrupted") {
		t.Fatalf("exit code = %d, stderr = %q, want 1 and interrupted", code, stderr.String())
	}
}

// TestRun_Stdout - без --output отчет печатается в stdout, предупреждения - только в stderr
func TestRun_Stdout(t *testing.T) {
	dir := t.TempDir()
	logFile := writeLog(t, dir, testLine+"\nbroken line\n")

	code, stdout, stderr := runCLI([]string{"--path", logFile, "--format", "json"}, "")
	if code != ExitOK {
		t.Fatalf("exit code = %d, want %d, stderr: %s", code, ExitOK, stderr)
	}
	if !json.Valid([]byte(stdout)) {
		t.Fatalf("stdout is not valid JSON:\n%s", stdout)
	}
	if !strings.Contains(stderr, "WARN: skipped malformed log line (") || !strings.Contains(stderr, "access.log:2") {
		t.Fatalf("no warning with position in stderr: %q", stderr)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("files in dir = %d, want only the log file", len(entries))
	}
}

// TestRun_Quiet - с --quiet вместо предупреждения на строку одна сводка в конце
func TestRun_Quiet(t *testing.T) {
	logFile := writeLog(t, t.TempDir(), "bad 1\n"+testLine+"\nbad 2\n")

	code, _, stderr := runCLI([]string{"--path", logFile, "-f", "json", "--quiet"}, "")
	if code != ExitOK {
		t.Fatalf("exit code = %d, stderr: %s", code, stderr)
	}
	if stderr != "WARN: skipped 2 malformed log lines\n" {
		t.Fatalf("stderr = %q, want one summary line", stderr)
	}
}

// TestRun_Stdin - --path - читает лог из stdin
func TestRun_Stdin(t *testing.T) {
	code, stdout, stderr := runCLI([]string{"--path", "-", "-f", "json"}, testLine+"\n"+testLine+"\n")
	if code != ExitOK {
		t.Fatalf("exit code = %d, stderr: %s", code, stderr)
	}

	var rep struct {
		Files              []string `json:"files"`
		TotalRequestsCount int      `json:"totalRequestsCount"`
	}
	if err := json.Unmarshal([]byte(stdout), &rep); err != nil {
		t.Fatal(err)
	}
	if rep.TotalRequestsCount != 2 || len(rep.Files) != 1 || rep.Files[0] != "stdin" {
		t.Fatalf("report = %+v, want 2 requests from stdin", rep)
	}
}

// TestRun_StdoutWriteError - ошибка печати отчета в stdout дает exit code 1
func TestRun_StdoutWriteError(t *testing.T) {
	logFile := writeLog(t, t.TempDir(), testLine+"\n")

	var stderr bytes.Buffer
	code := Run(context.Background(), []string{"--path", logFile}, Streams{Stdout: failWriter{}, Stderr: &stderr})
	if code != ExitInternal {
		t.Fatalf("exit code = %d, want %d", code, ExitInternal)
	}
}

// failWriter - writer, который всегда возвращает ошибку
type failWriter struct{}

// Write - всегда возвращает ошибку
func (failWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}

// TestRun_Comparison - поле comparison есть в JSON только с --compare-*
func TestRun_Comparison(t *testing.T) {
	logFile := writeLog(t, t.TempDir(), testLine+"\n")

	tests := []struct {
		name string
		args []string
		want bool
	}{
		{"without compare", []string{"--path", logFile, "-f", "json"}, false},
		{"with compare", []string{"--path", logFile, "-f", "json", "--compare-to", "2015-05-16"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, stdout, stderr := runCLI(tt.args, "")
			if code != ExitOK {
				t.Fatalf("exit code = %d, stderr: %s", code, stderr)
			}
			if got := strings.Contains(stdout, `"comparison"`); got != tt.want {
				t.Fatalf("has comparison = %v, want %v:\n%s", got, tt.want, stdout)
			}
		})
	}
}

// TestRun_Formats - Markdown и AsciiDoc содержат базовые и дополнительные разделы
func TestRun_Formats(t *testing.T) {
	logFile := writeLog(t, t.TempDir(), testLine+"\n")

	tests := []struct {
		format string
		want   []string
	}{
		{"markdown", []string{"# Incident Radar", "## Классы ответов", "## Временной ряд (шаг 1h)"}},
		{"adoc", []string{"= Incident Radar", "== Классы ответов", "== Временной ряд (шаг 1h)"}},
	}

	for _, tt := range tests {
		t.Run(tt.format, func(t *testing.T) {
			code, stdout, stderr := runCLI([]string{"--path", logFile, "-f", tt.format, "--bucket", "1h"}, "")
			if code != ExitOK {
				t.Fatalf("exit code = %d, stderr: %s", code, stderr)
			}
			for _, want := range tt.want {
				if !strings.Contains(stdout, want) {
					t.Fatalf("report has no %q:\n%s", want, stdout)
				}
			}
		})
	}
}
