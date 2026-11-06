package report

import (
	"bytes"
	"encoding/json"
	"maps"
	"strings"
	"testing"
	"time"

	"github.com/avequa/incident-radar/internal/parser"
	"github.com/avequa/incident-radar/internal/stats"
)

// result - статистика по набору записей с заданными кодами и размерами
func result(records ...parser.LogRecord) stats.Result {
	h := stats.NewHandlerLogLine()
	for _, r := range records {
		h.Add(r)
	}
	return h.Result()
}

// testComparison - основной период 3 запроса (200, 500, 500), сравниваемый 2 запроса (200, 404)
func testComparison(t *testing.T) *Comparison {
	t.Helper()
	from := time.Date(2025, time.March, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2025, time.March, 2, 0, 0, 0, 0, time.UTC)

	current := result(
		parser.LogRecord{Status: 200, Size: 100},
		parser.LogRecord{Status: 500, Size: 200},
		parser.LogRecord{Status: 500, Size: 301},
	)
	compared := result(
		parser.LogRecord{Status: 200, Size: 100},
		parser.LogRecord{Status: 404, Size: 200},
	)
	return NewComparison(stats.Period{To: &to}, stats.Period{From: &from, To: &to}, current, compared)
}

// TestNewComparison - delta считается как основной минус сравниваемый
func TestNewComparison(t *testing.T) {
	c := testComparison(t)

	if c.TotalRequestsCount != 2 {
		t.Fatalf("compared TotalRequestsCount = %d, want 2", c.TotalRequestsCount)
	}
	if c.Delta.TotalRequestsCount != 1 {
		t.Fatalf("delta TotalRequestsCount = %d, want 1", c.Delta.TotalRequestsCount)
	}
	if c.Delta.Average != 50.33 {
		t.Fatalf("delta Average = %v, want 50.33", c.Delta.Average)
	}
	if c.Delta.Max != 101 || c.Delta.P95 != 101 {
		t.Fatalf("delta Max = %d, P95 = %d, want 101 and 101", c.Delta.Max, c.Delta.P95)
	}

	wantCodes := map[int]int{200: 0, 404: -1, 500: 2}
	if !maps.Equal(c.Delta.ResponseCodes, wantCodes) {
		t.Fatalf("delta ResponseCodes = %v, want %v", c.Delta.ResponseCodes, wantCodes)
	}

	if c.CurrentPeriod.From != nil || *c.CurrentPeriod.To != "2025-03-02" {
		t.Fatalf("CurrentPeriod = %v - %v, want nil - 2025-03-02", c.CurrentPeriod.From, *c.CurrentPeriod.To)
	}
	if *c.ComparedPeriod.From != "2025-03-01" || *c.ComparedPeriod.To != "2025-03-02" {
		t.Fatalf("ComparedPeriod = %v - %v, want 2025-03-01 - 2025-03-02", *c.ComparedPeriod.From, *c.ComparedPeriod.To)
	}
}

// TestWriteJSON_Comparison - поле comparison есть в JSON только при сравнении
func TestWriteJSON_Comparison(t *testing.T) {
	tests := []struct {
		name       string
		comparison *Comparison
		want       bool
	}{
		{"without comparison", nil, false},
		{"with comparison", testComparison(t), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rep := testReport()
			rep.Comparison = tt.comparison

			var buf bytes.Buffer
			if err := WriteJSON(&buf, rep); err != nil {
				t.Fatal(err)
			}
			var got map[string]any
			if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
				t.Fatalf("invalid JSON: %v\n%s", err, buf.String())
			}

			comparison, ok := got["comparison"].(map[string]any)
			if ok != tt.want {
				t.Fatalf("has comparison = %v, want %v:\n%s", ok, tt.want, buf.String())
			}
			if !tt.want {
				return
			}
			for _, key := range []string{"currentPeriod", "comparedPeriod", "totalRequestsCount", "responseSizeInBytes", "responseCodes", "delta"} {
				if _, ok := comparison[key]; !ok {
					t.Fatalf("no field %q in comparison:\n%s", key, buf.String())
				}
			}
		})
	}
}

// TestComparisonTables - таблица сравнения в Markdown и AsciiDoc выводится только при сравнении
func TestComparisonTables(t *testing.T) {
	writers := []struct {
		name  string
		write func(*bytes.Buffer, Report) error
		title string
		row   string
	}{
		{
			"markdown", func(b *bytes.Buffer, r Report) error { return WriteMarkdown(b, r) },
			"## Сравнение периодов", "| Количество запросов | 3 | 2 | +1 |",
		},
		{
			"adoc", func(b *bytes.Buffer, r Report) error { return WriteAdoc(b, r) },
			"== Сравнение периодов", "|Количество запросов |3 |2 |+1",
		},
	}

	for _, w := range writers {
		t.Run(w.name, func(t *testing.T) {
			rep := testReport()

			var without bytes.Buffer
			if err := w.write(&without, rep); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(without.String(), w.title) {
				t.Fatalf("comparison table without --compare-*:\n%s", without.String())
			}

			rep.Comparison = testComparison(t)
			var with bytes.Buffer
			if err := w.write(&with, rep); err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{w.title, w.row} {
				if !strings.Contains(with.String(), want) {
					t.Fatalf("no %q:\n%s", want, with.String())
				}
			}
		})
	}
}
