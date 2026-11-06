// Package report описывает базовый отчет и пишет его в JSON, Markdown и AsciiDoc
package report

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/avequa/incident-radar/internal/stats"
)

// Report - базовый отчет: источники, счетчики строк, статистика и сравнение периодов
type Report struct {
	Files               []string `json:"files" jsonschema:"read sources in processing order"`
	FilesCount          int      `json:"filesCount"`
	ProcessedLinesCount int      `json:"processedLinesCount" jsonschema:"non-empty lines read, valid and malformed"`
	MalformedLinesCount int      `json:"malformedLinesCount" jsonschema:"skipped malformed lines"`
	stats.Result
	Comparison *Comparison `json:"comparison,omitempty" jsonschema:"present only when a compare period is set"`
}

// WriteJSON - пишет отчет в JSON с отступами
func WriteJSON(writer io.Writer, rep Report) error {
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	return encoder.Encode(rep)
}

// WriteMarkdown - пишет отчет в Markdown: заголовок и таблицы
func WriteMarkdown(writer io.Writer, rep Report) error {
	var sb strings.Builder

	sb.WriteString("# Incident Radar\n\n")

	sb.WriteString("## Общая информация\n\n")
	sb.WriteString("| Метрика | Значение |\n")
	sb.WriteString("|:---|---:|\n")
	fmt.Fprintf(&sb, "| Файлы | %s |\n", strings.Join(rep.Files, ", "))
	fmt.Fprintf(&sb, "| Количество файлов | %d |\n", rep.FilesCount)
	fmt.Fprintf(&sb, "| Обработано строк | %d |\n", rep.ProcessedLinesCount)
	fmt.Fprintf(&sb, "| Пропущено некорректных строк | %d |\n", rep.MalformedLinesCount)
	fmt.Fprintf(&sb, "| Количество запросов | %d |\n", rep.TotalRequestsCount)
	fmt.Fprintf(&sb, "| Средний размер ответа | %v |\n", rep.ResponseSizeInBytes.Average)
	fmt.Fprintf(&sb, "| Максимальный размер ответа | %d |\n", rep.ResponseSizeInBytes.Max)
	fmt.Fprintf(&sb, "| p95 размера ответа | %d |\n", rep.ResponseSizeInBytes.P95)

	sb.WriteString("\n## Ресурсы\n\n")
	sb.WriteString("| Ресурс | Количество |\n")
	sb.WriteString("|:---|---:|\n")
	for _, value := range rep.Resources {
		fmt.Fprintf(&sb, "| %s | %d |\n", value.Resource, value.Count)
	}

	sb.WriteString("\n## Коды ответа\n\n")
	sb.WriteString("| Код | Количество |\n")
	sb.WriteString("|:---|---:|\n")
	for _, code := range slices.Sorted(maps.Keys(rep.ResponseCodes)) {
		fmt.Fprintf(&sb, "| %s | %d |\n", StatusName(code), rep.ResponseCodes[code])
	}

	sb.WriteString("\n## Протоколы\n\n")
	sb.WriteString("| Протокол |\n")
	sb.WriteString("|:---|\n")
	for _, protocol := range rep.Protocols {
		fmt.Fprintf(&sb, "| %s |\n", protocol)
	}

	sb.WriteString("\n## Запросы по дням недели\n\n")
	sb.WriteString("| День недели | Количество |\n")
	sb.WriteString("|:---|---:|\n")
	for _, day := range rep.RequestsByWeekday {
		fmt.Fprintf(&sb, "| %s | %d |\n", day.Weekday, day.Count)
	}

	writeComparisonMarkdown(&sb, rep)

	_, err := io.WriteString(writer, sb.String())
	return err
}

// StatusName - код ответа с названием: 200 -> "200 OK", для неизвестного кода только число
func StatusName(code int) string {
	name := http.StatusText(code)
	if name == "" {
		return strconv.Itoa(code)
	}
	return strconv.Itoa(code) + " " + name
}

// WriteAdoc - пишет отчет в AsciiDoc с теми же данными и таблицами, что Markdown
func WriteAdoc(writer io.Writer, rep Report) error {
	var sb strings.Builder

	sb.WriteString("= Incident Radar\n\n")

	sb.WriteString("== Общая информация\n\n")
	sb.WriteString("[cols=\"<,>\",options=\"header\"]\n|===\n")
	sb.WriteString("|Метрика |Значение\n")
	fmt.Fprintf(&sb, "|Файлы |%s\n", strings.Join(rep.Files, ", "))
	fmt.Fprintf(&sb, "|Количество файлов |%d\n", rep.FilesCount)
	fmt.Fprintf(&sb, "|Обработано строк |%d\n", rep.ProcessedLinesCount)
	fmt.Fprintf(&sb, "|Пропущено некорректных строк |%d\n", rep.MalformedLinesCount)
	fmt.Fprintf(&sb, "|Количество запросов |%d\n", rep.TotalRequestsCount)
	fmt.Fprintf(&sb, "|Средний размер ответа |%v\n", rep.ResponseSizeInBytes.Average)
	fmt.Fprintf(&sb, "|Максимальный размер ответа |%d\n", rep.ResponseSizeInBytes.Max)
	fmt.Fprintf(&sb, "|p95 размера ответа |%d\n", rep.ResponseSizeInBytes.P95)
	sb.WriteString("|===\n")

	sb.WriteString("\n== Ресурсы\n\n")
	sb.WriteString("[cols=\"<,>\",options=\"header\"]\n|===\n")
	sb.WriteString("|Ресурс |Количество\n")
	for _, value := range rep.Resources {
		fmt.Fprintf(&sb, "|%s |%d\n", value.Resource, value.Count)
	}
	sb.WriteString("|===\n")

	sb.WriteString("\n== Коды ответа\n\n")
	sb.WriteString("[cols=\"<,>\",options=\"header\"]\n|===\n")
	sb.WriteString("|Код |Количество\n")
	for _, code := range slices.Sorted(maps.Keys(rep.ResponseCodes)) {
		fmt.Fprintf(&sb, "|%s |%d\n", StatusName(code), rep.ResponseCodes[code])
	}
	sb.WriteString("|===\n")

	sb.WriteString("\n== Протоколы\n\n")
	sb.WriteString("[cols=\"<\",options=\"header\"]\n|===\n")
	sb.WriteString("|Протокол\n")
	for _, protocol := range rep.Protocols {
		fmt.Fprintf(&sb, "|%s\n", protocol)
	}
	sb.WriteString("|===\n")

	sb.WriteString("\n== Запросы по дням недели\n\n")
	sb.WriteString("[cols=\"<,>\",options=\"header\"]\n|===\n")
	sb.WriteString("|День недели |Количество\n")
	for _, day := range rep.RequestsByWeekday {
		fmt.Fprintf(&sb, "|%s |%d\n", day.Weekday, day.Count)
	}
	sb.WriteString("|===\n")

	writeComparisonAdoc(&sb, rep)

	_, err := io.WriteString(writer, sb.String())
	return err
}
