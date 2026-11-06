package parser

import (
	"errors"
	"testing"
	"time"
)

// TestParseLine - эталонная строка разбирается в корректные поля LogRecord
func TestParseLine(t *testing.T) {
	line := `93.180.71.3 - - [17/May/2015:08:05:32 +0000] "GET /downloads/product_1 HTTP/1.1" 304 0 "-" "Debian APT-HTTP/1.3"`

	got, err := ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := LogRecord{
		IP:       "93.180.71.3",
		Time:     time.Date(2015, time.May, 17, 8, 5, 32, 0, time.UTC),
		Method:   "GET",
		Resource: "/downloads/product_1",
		Protocol: "HTTP/1.1",
		Status:   304,
		Size:     0,
	}
	if !got.Time.Equal(want.Time) {
		t.Fatalf("Time = %v, want %v", got.Time, want.Time)
	}
	got.Time = want.Time
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

// TestParseLine_BadLine - поврежденные строки возвращают ErrBadLine
func TestParseLine_BadLine(t *testing.T) {
	tests := []struct {
		name string
		line string
	}{
		{"empty", ""},
		{"not a log", "this is not a log line"},
		{"truncated", `93.180.71.3 - - [17/May/2015:08:05:32 +0000] "GET /downloads/product_1 HTTP/1.1" 304`},
		{"bad date", `93.180.71.3 - - [32/Foo/2015:08:05:32 +0000] "GET / HTTP/1.1" 304 0 "-" "ua"`},
		{"bad request", `93.180.71.3 - - [17/May/2015:08:05:32 +0000] "-" 400 0 "-" "ua"`},
		{"bad status", `93.180.71.3 - - [17/May/2015:08:05:32 +0000] "GET / HTTP/1.1" 999 0 "-" "ua"`},
		{"size overflow", `93.180.71.3 - - [17/May/2015:08:05:32 +0000] "GET / HTTP/1.1" 200 99999999999999999999 "-" "ua"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseLine(tt.line)
			if !errors.Is(err, ErrBadLine) {
				t.Fatalf("expected ErrBadLine, got %v", err)
			}
		})
	}
}
