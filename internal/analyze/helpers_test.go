package analyze

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testLine = `93.180.71.3 - - [17/May/2015:08:05:32 +0000] "GET /downloads/product_1 HTTP/1.1" 304 0 "-" "Debian APT-HTTP/1.3"`

// logLine - корректная строка лога с заданными временем, IP, методом, ресурсом, кодом и размером
func logLine(t time.Time, ip, method, resource string, status, size int) string {
	return fmt.Sprintf(`%s - - [%s] "%s %s HTTP/1.1" %d %d "-" "test"`,
		ip, t.Format("02/Jan/2006:15:04:05 -0700"), method, resource, status, size)
}

// at - время 17 мая 2015 года в UTC
func at(hour, minute, second int) time.Time {
	return time.Date(2015, time.May, 17, hour, minute, second, 0, time.UTC)
}

// writeFile - создает файл во временной папке и возвращает путь к нему
func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// runStdin - запускает анализ лога из строки через stdin
func runStdin(t *testing.T, content string, opts Options) (Report, string, error) {
	t.Helper()
	var warnings strings.Builder
	opts.Path = StdinPath
	opts.Stdin = strings.NewReader(content)
	opts.Warnings = &warnings
	rep, err := Run(context.Background(), opts)
	return rep, warnings.String(), err
}

// runReader - запускает анализ лога из произвольного io.Reader
func runReader(t *testing.T, r io.Reader) (Report, string) {
	t.Helper()
	var warnings strings.Builder
	rep, err := Run(context.Background(), Options{Path: StdinPath, Stdin: r, Warnings: &warnings})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return rep, warnings.String()
}

// repeatReader - управляемый io.Reader: отдает n одинаковых байт, не держа их в памяти
type repeatReader struct {
	b byte
	n int
}

// Read - заполняет p байтом b, пока не отдаст n байт
func (r *repeatReader) Read(p []byte) (int, error) {
	if r.n == 0 {
		return 0, io.EOF
	}
	k := min(len(p), r.n)
	for i := range k {
		p[i] = r.b
	}
	r.n -= k
	return k, nil
}
