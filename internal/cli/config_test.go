package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestParse_UsageErrors - неверные аргументы возвращают ErrUsage
func TestParse_UsageErrors(t *testing.T) {
	dir := t.TempDir()
	mdOut := filepath.Join(dir, "report.md")
	jsonOut := filepath.Join(dir, "report.json")

	existing := filepath.Join(dir, "existing.md")
	if err := os.WriteFile(existing, []byte("old report"), 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		args []string
	}{
		{"no path", []string{"--output", mdOut}},
		{"unsupported format xml", []string{"--path", "a.log", "--format", "xml", "--output", mdOut}},
		{"unknown flag", []string{"--path", "a.log", "--output", mdOut, "--xyz"}},
		{"invalid from date", []string{"--path", "a.log", "--output", mdOut, "--from", "2025-13-01"}},
		{"invalid to date", []string{"--path", "a.log", "--output", mdOut, "--to", "01.03.2025"}},
		{"from equals to", []string{"--path", "a.log", "--output", mdOut, "--from", "2025-03-01", "--to", "2025-03-01"}},
		{"from after to", []string{"--path", "a.log", "--output", mdOut, "--from", "2025-03-05", "--to", "2025-03-01"}},
		{"json format with .md output", []string{"--path", "a.log", "--format", "json", "--output", mdOut}},
		{"markdown format with .json output", []string{"--path", "a.log", "--format", "markdown", "--output", jsonOut}},
		{"adoc format with .md output", []string{"--path", "a.log", "--format", "adoc", "--output", mdOut}},
		{"existing output file", []string{"--path", "a.log", "--output", existing}},
		{"extra arguments", []string{"--path", "a.log", "b.log", "--output", mdOut}},
		{"invalid compare-from date", []string{"--path", "a.log", "--output", mdOut, "--compare-from", "2025-13-01"}},
		{"invalid compare-to date", []string{"--path", "a.log", "--output", mdOut, "--compare-to", "01.03.2025"}},
		{"compare-from equals compare-to", []string{"--path", "a.log", "--output", mdOut, "--compare-from", "2025-03-01", "--compare-to", "2025-03-01"}},
		{"invalid since", []string{"--path", "a.log", "--since", "2015-05-17 08:00"}},
		{"invalid until", []string{"--path", "a.log", "--until", "yesterday"}},
		{"since after until", []string{"--path", "a.log", "--since", "2015-05-17T09:00:00Z", "--until", "2015-05-17T08:00:00Z"}},
		{"invalid bucket", []string{"--path", "a.log", "--bucket", "5 minutes"}},
		{"zero top", []string{"--path", "a.log", "--top", "0"}},
		{"not a number top", []string{"--path", "a.log", "--top", "ten"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stderr bytes.Buffer
			_, err := ParseCliFlag(tt.args, &stderr)
			if !errors.Is(err, ErrUsage) {
				t.Fatalf("expected ErrUsage, got %v", err)
			}
		})
	}
}

// TestParse_ExistingOutputNotChanged - проверка output не изменяет существующий файл
func TestParse_ExistingOutputNotChanged(t *testing.T) {
	existing := filepath.Join(t.TempDir(), "report.md")
	if err := os.WriteFile(existing, []byte("old report"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, _ = ParseCliFlag([]string{"--path", "a.log", "--output", existing}, &bytes.Buffer{})

	got, err := os.ReadFile(existing) //nolint:gosec // путь из t.TempDir()
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "old report" {
		t.Fatalf("output file was changed: %q", got)
	}
}

// TestParse_Valid - корректные аргументы заполняют поля UserInput
func TestParse_Valid(t *testing.T) {
	dir := t.TempDir()
	mdOut := filepath.Join(dir, "report.md")
	jsonOut := filepath.Join(dir, "report.json")
	adocOut := filepath.Join(dir, "report.adoc")

	tests := []struct {
		name string
		args []string
		want UserInput
	}{
		{
			name: "default format is markdown",
			args: []string{"--path", "logs/*.log", "--output", mdOut},
			want: UserInput{Path: "logs/*.log", Format: FormatMarkdown, Output: mdOut},
		},
		{
			name: "short flags and json",
			args: []string{"-p", "access.log", "-f", "json", "-o", jsonOut},
			want: UserInput{Path: "access.log", Format: FormatJSON, Output: jsonOut},
		},
		{
			name: "adoc format",
			args: []string{"--path", "a.log", "-f", "adoc", "-o", adocOut},
			want: UserInput{Path: "a.log", Format: FormatAdoc, Output: adocOut},
		},
		{
			name: "url path",
			args: []string{"--path", "http://127.0.0.1:8000/access.txt", "--output", mdOut},
			want: UserInput{Path: "http://127.0.0.1:8000/access.txt", Format: FormatMarkdown, Output: mdOut},
		},
		{
			name: "no output - stdout",
			args: []string{"--path", "a.log", "-f", "json"},
			want: UserInput{Path: "a.log", Format: FormatJSON, Output: ""},
		},
		{
			name: "only from",
			args: []string{"--path", "a.log", "--output", mdOut, "--from", "2025-03-01"},
			want: UserInput{Path: "a.log", Format: FormatMarkdown, Output: mdOut, From: mustDate(t, "2025-03-01")},
		},
		{
			name: "only to",
			args: []string{"--path", "a.log", "--output", mdOut, "--to", "2025-03-31"},
			want: UserInput{Path: "a.log", Format: FormatMarkdown, Output: mdOut, To: mustDate(t, "2025-03-31")},
		},
		{
			name: "both dates",
			args: []string{"--path", "a.log", "--output", mdOut, "--from", "2025-03-01", "--to", "2025-03-31"},
			want: UserInput{
				Path: "a.log", Format: FormatMarkdown, Output: mdOut,
				From: mustDate(t, "2025-03-01"), To: mustDate(t, "2025-03-31"),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseCliFlag(tt.args, &bytes.Buffer{})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Path != tt.want.Path || got.Format != tt.want.Format || got.Output != tt.want.Output {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
			if !sameDate(got.From, tt.want.From) {
				t.Fatalf("From: got %v, want %v", got.From, tt.want.From)
			}
			if !sameDate(got.To, tt.want.To) {
				t.Fatalf("To: got %v, want %v", got.To, tt.want.To)
			}
		})
	}
}

// mustDate - разбирает дату YYYY-MM-DD для ожидаемых значений
func mustDate(t *testing.T, s string) *time.Time {
	t.Helper()
	d, err := time.Parse(datePattern, s)
	if err != nil {
		t.Fatal(err)
	}
	return &d
}

// sameDate - оба указателя nil или указывают на равное время
func sameDate(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Equal(*b)
}

// TestParse_ComparePeriod - период сравнения с двумя, одной и без границ
func TestParse_ComparePeriod(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		wantFrom    *time.Time
		wantTo      *time.Time
		wantCompare bool
	}{
		{
			name:     "both compare dates",
			args:     []string{"--path", "a.log", "--compare-from", "2025-03-01", "--compare-to", "2025-03-02"},
			wantFrom: mustDate(t, "2025-03-01"), wantTo: mustDate(t, "2025-03-02"), wantCompare: true,
		},
		{
			name:   "only compare-to",
			args:   []string{"--path", "a.log", "--compare-to", "2025-03-02"},
			wantTo: mustDate(t, "2025-03-02"), wantCompare: true,
		},
		{
			name: "no compare",
			args: []string{"--path", "a.log"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseCliFlag(tt.args, &bytes.Buffer{})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !sameDate(got.CompareFrom, tt.wantFrom) || !sameDate(got.CompareTo, tt.wantTo) {
				t.Fatalf("compare period = %v - %v, want %v - %v", got.CompareFrom, got.CompareTo, tt.wantFrom, tt.wantTo)
			}
			if gotCompare := got.CompareFrom != nil || got.CompareTo != nil; gotCompare != tt.wantCompare {
				t.Fatalf("compare = %v, want %v", gotCompare, tt.wantCompare)
			}
		})
	}
}

// TestParse_AgentFlags - --since/--until с часовым поясом, --bucket, --top, --quiet и --path - для stdin
func TestParse_AgentFlags(t *testing.T) {
	args := []string{
		"--path", StdinPath, "-f", "json", "-q",
		"--since", "2015-05-17T08:00:00Z", "--until", "2015-05-17T11:00:00+02:00",
		"--bucket", "5m", "--top", "3",
	}

	got, err := ParseCliFlag(args, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got.Path != StdinPath || !got.Quiet || got.Bucket != 5*time.Minute || got.Top != 3 {
		t.Fatalf("got %+v", got)
	}
	since := time.Date(2015, time.May, 17, 8, 0, 0, 0, time.UTC)
	until := time.Date(2015, time.May, 17, 9, 0, 0, 0, time.UTC)
	if !sameDate(got.Since, &since) || !sameDate(got.Until, &until) {
		t.Fatalf("window = %v - %v, want %v - %v", got.Since, got.Until, since, until)
	}
}

// TestParse_Defaults - без флагов окна top = 10, без timeline и без quiet
func TestParse_Defaults(t *testing.T) {
	got, err := ParseCliFlag([]string{"--path", "a.log"}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Top != 10 || got.Bucket != 0 || got.Quiet || got.Since != nil || got.Until != nil {
		t.Fatalf("got %+v", got)
	}
}
