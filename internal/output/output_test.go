package output

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestWriteNew_CreatesFile - отчет сохраняется в новый файл
func TestWriteNew_CreatesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.md")

	if err := WriteNew(path, []byte("report")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, err := os.ReadFile(path) //nolint:gosec // путь из t.TempDir()
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "report" {
		t.Fatalf("file content = %q, want %q", got, "report")
	}
}

// TestWriteNew_ExistingFileNotChanged - существующий файл не изменяется, возвращается ErrExists
func TestWriteNew_ExistingFileNotChanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.md")
	if err := os.WriteFile(path, []byte("old report"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := WriteNew(path, []byte("new report"))
	if !errors.Is(err, ErrExists) {
		t.Fatalf("err = %v, want ErrExists", err)
	}

	got, err := os.ReadFile(path) //nolint:gosec // путь из t.TempDir()
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "old report" {
		t.Fatalf("existing file was changed: %q", got)
	}
}

// TestWriteNew_CreatesDir - недостающие папки создаются, отчет записывается
func TestWriteNew_CreatesDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reports", "today", "report.md")

	if err := WriteNew(path, []byte("report")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, err := os.ReadFile(path) //nolint:gosec // путь из t.TempDir()
	if err != nil {
		t.Fatalf("report was not created: %v", err)
	}
	if string(got) != "report" {
		t.Fatalf("file content = %q, want %q", got, "report")
	}
}

// TestWriteNew_DirIsFile - если на месте папки лежит файл, возвращается ошибка, а не ErrExists
func TestWriteNew_DirIsFile(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "reports")
	if err := os.WriteFile(blocker, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := WriteNew(filepath.Join(blocker, "report.md"), []byte("report"))
	if err == nil || errors.Is(err, ErrExists) {
		t.Fatalf("err = %v, want directory error", err)
	}
}

// TestWriteNew_OutputIsDir - если output - это папка, возвращается ErrExists
func TestWriteNew_OutputIsDir(t *testing.T) {
	err := WriteNew(t.TempDir(), []byte("report"))
	if !errors.Is(err, ErrExists) {
		t.Fatalf("err = %v, want ErrExists", err)
	}
}
