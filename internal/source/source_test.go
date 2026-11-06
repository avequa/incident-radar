package source

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

const testLog = `93.180.71.3 - - [17/May/2015:08:05:32 +0000] "GET /downloads/product_1 HTTP/1.1" 304 0 "-" "Debian APT-HTTP/1.3"` + "\n"

// writeFile - создает файл во временной папке и возвращает путь к нему
func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// readStream - открывает источник, читает его целиком и закрывает
func readStream(t *testing.T, client *http.Client, name string) string {
	t.Helper()
	stream, err := OpenStream(context.Background(), client, name)
	if err != nil {
		t.Fatalf("OpenStream(%q): %v", name, err)
	}
	defer func() { _ = stream.Close() }()

	data, err := io.ReadAll(stream)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestLocalFile - локальный файл находится и читается
func TestLocalFile(t *testing.T) {
	p := writeFile(t, t.TempDir(), "access.log", testLog)

	files, err := FindSources(p)
	if err != nil {
		t.Fatalf("FindSources: %v", err)
	}
	if !slices.Equal(files, []string{p}) {
		t.Fatalf("files = %v, want [%s]", files, p)
	}

	if got := readStream(t, http.DefaultClient, p); got != testLog {
		t.Fatalf("content = %q, want %q", got, testLog)
	}
}

// TestURL - URL находится и читается
func TestURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, testLog)
	}))
	defer server.Close()

	u := server.URL + "/access.txt"

	files, err := FindSources(u)
	if err != nil {
		t.Fatalf("FindSources: %v", err)
	}
	if !slices.Equal(files, []string{u}) {
		t.Fatalf("files = %v, want [%s]", files, u)
	}

	if got := readStream(t, server.Client(), u); got != testLog {
		t.Fatalf("content = %q, want %q", got, testLog)
	}
}

// TestGlobFindsAllFiles - glob находит все .log и .txt файлы в порядке имен
func TestGlobFindsAllFiles(t *testing.T) {
	dir := t.TempDir()
	b := writeFile(t, dir, "b.log", testLog)
	a := writeFile(t, dir, "a.txt", testLog)
	writeFile(t, dir, "notes.md", "not a log")

	files, err := FindSources(filepath.Join(dir, "*"))
	if err != nil {
		t.Fatalf("FindSources: %v", err)
	}

	want := []string{a, b}
	if !slices.Equal(files, want) {
		t.Fatalf("files = %v, want %v", files, want)
	}
}

// TestSourceErrors - отсутствующий файл, пустой glob, HTTP 404 и неверное расширение дают ErrSource
func TestSourceErrors(t *testing.T) {
	dir := t.TempDir()

	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()

	t.Run("missing file", func(t *testing.T) {
		_, err := OpenStream(context.Background(), http.DefaultClient, filepath.Join(dir, "nope.log"))
		if !errors.Is(err, ErrSource) {
			t.Fatalf("expected ErrSource, got %v", err)
		}
	})

	t.Run("empty glob", func(t *testing.T) {
		_, err := FindSources(filepath.Join(dir, "*.log"))
		if !errors.Is(err, ErrSource) {
			t.Fatalf("expected ErrSource, got %v", err)
		}
	})

	t.Run("http 404", func(t *testing.T) {
		_, err := OpenStream(context.Background(), server.Client(), server.URL+"/access.txt")
		if !errors.Is(err, ErrSource) {
			t.Fatalf("expected ErrSource, got %v", err)
		}
	})

	t.Run("unsupported extension", func(t *testing.T) {
		for _, path := range []string{"report.md", server.URL + "/access.csv"} {
			if _, err := FindSources(path); !errors.Is(err, ErrSource) {
				t.Fatalf("FindSources(%q): expected ErrSource, got %v", path, err)
			}
		}
	})

	t.Run("bad glob", func(t *testing.T) {
		if _, err := FindSources(filepath.Join(dir, "[.log")); !errors.Is(err, ErrSource) {
			t.Fatalf("expected ErrSource, got %v", err)
		}
	})

	t.Run("unreachable url", func(t *testing.T) {
		_, err := OpenStream(context.Background(), http.DefaultClient, "http://127.0.0.1:1/access.log")
		if !errors.Is(err, ErrSource) {
			t.Fatalf("expected ErrSource, got %v", err)
		}
	})
}
