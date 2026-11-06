package report

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/avequa/incident-radar/internal/parser"
	"github.com/avequa/incident-radar/internal/stats"
)

// testReport - отчет из 3 записей: коды 200 и 404, HTTP/1.1 и HTTP/2.0, понедельник и воскресенье
func testReport() Report {
	handler := stats.NewHandlerLogLine()
	monday := time.Date(2025, time.March, 3, 10, 0, 0, 0, time.UTC)
	sunday := time.Date(2025, time.March, 9, 10, 0, 0, 0, time.UTC)

	handler.Add(parser.LogRecord{Time: monday, Resource: "/a", Protocol: "HTTP/1.1", Status: 200, Size: 100})
	handler.Add(parser.LogRecord{Time: monday, Resource: "/a", Protocol: "HTTP/1.1", Status: 200, Size: 200})
	handler.Add(parser.LogRecord{Time: sunday, Resource: "/b", Protocol: "HTTP/2.0", Status: 404, Size: 300})

	return Report{
		Files:               []string{"a.log"},
		FilesCount:          1,
		ProcessedLinesCount: 4,
		MalformedLinesCount: 1,
		Result:              handler.Result(),
	}
}

// TestWriteJSON - JSON-отчет валиден и содержит все обязательные поля
func TestWriteJSON(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteJSON(&buf, testReport()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, buf.String())
	}

	for _, key := range []string{"files", "totalRequestsCount", "responseSizeInBytes", "resources", "responseCodes", "protocols", "requestsByWeekday"} {
		if _, ok := got[key]; !ok {
			t.Fatalf("no field %q in JSON:\n%s", key, buf.String())
		}
	}

	size, ok := got["responseSizeInBytes"].(map[string]any)
	if !ok {
		t.Fatalf("responseSizeInBytes is not an object:\n%s", buf.String())
	}
	for _, key := range []string{"average", "max", "p95"} {
		if _, ok := size[key]; !ok {
			t.Fatalf("no field %q in responseSizeInBytes:\n%s", key, buf.String())
		}
	}
}

// TestWriteMarkdown - Markdown-отчет содержит заголовок и таблицы
func TestWriteMarkdown(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteMarkdown(&buf, testReport()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	md := buf.String()

	if !strings.HasPrefix(md, "# ") {
		t.Fatalf("no title:\n%s", md)
	}
	for _, header := range []string{"| Метрика | Значение |", "| Ресурс | Количество |", "| Код | Количество |", "| Протокол |", "| День недели | Количество |"} {
		if !strings.Contains(md, header) {
			t.Fatalf("no table %q:\n%s", header, md)
		}
	}
}

// TestReportsSameData - JSON, Markdown и AsciiDoc содержат одинаковые данные
func TestReportsSameData(t *testing.T) {
	rep := testReport()

	var jsonBuf, mdBuf, adocBuf bytes.Buffer
	if err := WriteJSON(&jsonBuf, rep); err != nil {
		t.Fatal(err)
	}
	if err := WriteMarkdown(&mdBuf, rep); err != nil {
		t.Fatal(err)
	}
	if err := WriteAdoc(&adocBuf, rep); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		json string
		md   string
		adoc string
	}{
		{"files", `"a.log"`, "| Файлы | a.log |", "|Файлы |a.log"},
		{"total requests", `"totalRequestsCount": 3`, "| Количество запросов | 3 |", "|Количество запросов |3"},
		{"average", `"average": 200`, "| Средний размер ответа | 200 |", "|Средний размер ответа |200"},
		{"max", `"max": 300`, "| Максимальный размер ответа | 300 |", "|Максимальный размер ответа |300"},
		{"p95", `"p95": 300`, "| p95 размера ответа | 300 |", "|p95 размера ответа |300"},
		{"resource", `"resource": "/a"`, "| /a | 2 |", "|/a |2"},
		{"code 200", `"200": 2`, "| 200 OK | 2 |", "|200 OK |2"},
		{"code 404", `"404": 1`, "| 404 Not Found | 1 |", "|404 Not Found |1"},
		{"protocol HTTP/1.1", `"HTTP/1.1"`, "| HTTP/1.1 |", "|HTTP/1.1"},
		{"protocol HTTP/2.0", `"HTTP/2.0"`, "| HTTP/2.0 |", "|HTTP/2.0"},
		{"monday", `"weekday": "Monday"`, "| Monday | 2 |", "|Monday |2"},
		{"sunday", `"weekday": "Sunday"`, "| Sunday | 1 |", "|Sunday |1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !strings.Contains(jsonBuf.String(), tt.json) {
				t.Fatalf("JSON has no %q:\n%s", tt.json, jsonBuf.String())
			}
			if !strings.Contains(mdBuf.String(), tt.md) {
				t.Fatalf("Markdown has no %q:\n%s", tt.md, mdBuf.String())
			}
			if !strings.Contains(adocBuf.String(), tt.adoc) {
				t.Fatalf("AsciiDoc has no %q:\n%s", tt.adoc, adocBuf.String())
			}
		})
	}
}

// TestWriteAdoc - AsciiDoc-отчет содержит заголовок и таблицы
func TestWriteAdoc(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteAdoc(&buf, testReport()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	adoc := buf.String()

	if !strings.HasPrefix(adoc, "= ") {
		t.Fatalf("no title:\n%s", adoc)
	}
	for _, header := range []string{"|Метрика |Значение", "|Ресурс |Количество", "|Код |Количество", "|Протокол", "|День недели |Количество"} {
		if !strings.Contains(adoc, header) {
			t.Fatalf("no table %q:\n%s", header, adoc)
		}
	}
	if got := strings.Count(adoc, "|===\n"); got != 10 {
		t.Fatalf("table borders = %d, want 10:\n%s", got, adoc)
	}
}

// TestStatusName - названия HTTP-кодов, неизвестный код выводится без названия
func TestStatusName(t *testing.T) {
	tests := []struct {
		code int
		want string
	}{
		{200, "200 OK"},
		{404, "404 Not Found"},
		{500, "500 Internal Server Error"},
		{299, "299"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := StatusName(tt.code); got != tt.want {
				t.Fatalf("StatusName(%d) = %q, want %q", tt.code, got, tt.want)
			}
		})
	}
}
