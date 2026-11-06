package analyze

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestWriteJSON_Fields - JSON содержит schemaVersion, базовые и дополнительные поля
func TestWriteJSON_Fields(t *testing.T) {
	rep, _, err := runStdin(t, testLine, Options{Bucket: time.Minute, Since: new(at(8, 0, 0))})
	if err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := WriteJSON(&buf, rep); err != nil {
		t.Fatal(err)
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(buf.Bytes(), &fields); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{
		"schemaVersion", "files", "totalRequestsCount", "responseCodes", "resources", "responseSizeInBytes",
		"timeRange", "window", "statusClasses", "errorRate", "clientErrorRate", "methods", "topIPs",
		"topErrorResources", "timeline",
	} {
		if _, ok := fields[key]; !ok {
			t.Fatalf("JSON has no %q:\n%s", key, buf.String())
		}
	}
}

// TestWriteMarkdown_Extra - Markdown содержит дополнительные разделы и экранирует "|" в ячейках
func TestWriteMarkdown_Extra(t *testing.T) {
	line := strings.Replace(testLine, "304", "500", 1)
	line = strings.Replace(line, "/downloads/product_1", "/a|b", 1)
	rep, _, err := runStdin(t, line, Options{Bucket: time.Minute})
	if err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := WriteMarkdown(&buf, rep); err != nil {
		t.Fatal(err)
	}
	md := buf.String()
	for _, want := range []string{
		"# Incident Radar", "## Окно анализа", "## Классы ответов", "| Доля 5xx (errorRate) | 1 |",
		"## HTTP-методы", "## Top IP", "| 93.180.71.3 | 1 |", "## Ресурсы с ответами 5xx", `| /a\|b | 1 |`,
		"## Временной ряд (шаг 1m)", "| 2015-05-17T08:05:00Z | 1 | 0 | 0 | 0 | 1 | 1 | 0 |",
	} {
		if !strings.Contains(md, want) {
			t.Fatalf("markdown has no %q:\n%s", want, md)
		}
	}
}

// TestWriteAdoc_Extra - AsciiDoc содержит дополнительные разделы с корректными таблицами
func TestWriteAdoc_Extra(t *testing.T) {
	rep, _, err := runStdin(t, testLine, Options{})
	if err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := WriteAdoc(&buf, rep); err != nil {
		t.Fatal(err)
	}
	adoc := buf.String()
	for _, want := range []string{"= Incident Radar", "== Классы ответов", "[cols=\"<,>\",options=\"header\"]", "|3xx |1"} {
		if !strings.Contains(adoc, want) {
			t.Fatalf("adoc has no %q:\n%s", want, adoc)
		}
	}
	if strings.Contains(adoc, "Временной ряд") {
		t.Fatal("adoc has a timeline without bucket")
	}
	if got := strings.Count(adoc, "|===\n"); got%2 != 0 {
		t.Fatalf("unbalanced tables: %d delimiters", got)
	}
}
