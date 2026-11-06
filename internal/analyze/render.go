package analyze

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/avequa/incident-radar/internal/report"
)

// WriteJSON - пишет полный отчет в JSON с отступами
func WriteJSON(w io.Writer, rep Report) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(rep)
}

// WriteMarkdown - пишет базовый отчет и дополнительные разделы в Markdown
func WriteMarkdown(w io.Writer, rep Report) error {
	if err := report.WriteMarkdown(w, rep.Report); err != nil {
		return err
	}
	var sb strings.Builder
	for _, t := range extraTables(rep) {
		t.markdown(&sb)
	}
	_, err := io.WriteString(w, sb.String())
	return err
}

// WriteAdoc - пишет базовый отчет и дополнительные разделы в AsciiDoc
func WriteAdoc(w io.Writer, rep Report) error {
	if err := report.WriteAdoc(w, rep.Report); err != nil {
		return err
	}
	var sb strings.Builder
	for _, t := range extraTables(rep) {
		t.adoc(&sb)
	}
	_, err := io.WriteString(w, sb.String())
	return err
}

// table - раздел отчета с одной таблицей, общий для Markdown и AsciiDoc
type table struct {
	title   string
	headers []string
	rows    [][]string
}

// extraTables - разделы, которых нет в базовом отчете: окно, классы ответов, методы, IP, 5xx, временной ряд
func extraTables(rep Report) []table {
	classes := rep.StatusClasses
	tables := []table{
		{
			title:   "Окно анализа",
			headers: []string{"Параметр", "Значение"},
			rows: [][]string{
				{"Первая запись в логах", timeRangeValue(rep.TimeRange, true)},
				{"Последняя запись в логах", timeRangeValue(rep.TimeRange, false)},
				{"since", windowValue(rep.Window, true)},
				{"until", windowValue(rep.Window, false)},
			},
		},
		{
			title:   "Классы ответов",
			headers: []string{"Класс", "Количество"},
			rows: [][]string{
				{"1xx", itoa(classes.Informational)},
				{"2xx", itoa(classes.Success)},
				{"3xx", itoa(classes.Redirection)},
				{"4xx", itoa(classes.ClientError)},
				{"5xx", itoa(classes.ServerError)},
				{"Доля 5xx (errorRate)", ftoa(rep.ErrorRate)},
				{"Доля 4xx (clientErrorRate)", ftoa(rep.ClientErrorRate)},
			},
		},
		countTable("HTTP-методы", "Метод", rep.Methods, func(m MethodCount) (string, int) { return m.Method, m.Count }),
		countTable("Top IP", "IP", rep.TopIPs, func(ip IPCount) (string, int) { return ip.IP, ip.Count }),
		countTable("Ресурсы с ответами 5xx", "Ресурс", rep.TopErrorResources,
			func(r ErrorResourceCount) (string, int) { return r.Resource, r.Count }),
	}

	if rep.Timeline != nil {
		t := table{
			title:   "Временной ряд (шаг " + rep.Timeline.Bucket + ")",
			headers: []string{"Начало", "Запросы", "2xx", "3xx", "4xx", "5xx", "errorRate", "p95 размера"},
		}
		for _, p := range rep.Timeline.Points {
			t.rows = append(t.rows, []string{
				p.Start, itoa(p.Requests), itoa(p.Success), itoa(p.Redirection),
				itoa(p.ClientError), itoa(p.ServerError), ftoa(p.ErrorRate), itoa(p.P95SizeBytes),
			})
		}
		tables = append(tables, t)
	}
	return tables
}

// countTable - таблица "имя - количество" из среза счетчиков
func countTable[T any](title, nameHeader string, items []T, get func(T) (string, int)) table {
	t := table{title: title, headers: []string{nameHeader, "Количество"}}
	for _, item := range items {
		name, count := get(item)
		t.rows = append(t.rows, []string{name, itoa(count)})
	}
	return t
}

// markdown - пишет раздел в Markdown, первая колонка выравнивается влево, остальные вправо
func (t table) markdown(sb *strings.Builder) {
	fmt.Fprintf(sb, "\n## %s\n\n", t.title)
	sb.WriteString("| " + strings.Join(t.headers, " | ") + " |\n")
	sb.WriteString("|:---" + strings.Repeat("|---:", len(t.headers)-1) + "|\n")
	for _, row := range t.rows {
		cells := make([]string, len(row))
		for i, cell := range row {
			cells[i] = escapeCell(cell)
		}
		sb.WriteString("| " + strings.Join(cells, " | ") + " |\n")
	}
}

// adoc - пишет раздел в AsciiDoc, первая колонка выравнивается влево, остальные вправо
func (t table) adoc(sb *strings.Builder) {
	fmt.Fprintf(sb, "\n== %s\n\n", t.title)
	fmt.Fprintf(sb, "[cols=\"<%s\",options=\"header\"]\n|===\n", strings.Repeat(",>", len(t.headers)-1))
	sb.WriteString("|" + strings.Join(t.headers, " |") + "\n")
	for _, row := range t.rows {
		cells := make([]string, len(row))
		for i, cell := range row {
			cells[i] = escapeCell(cell)
		}
		sb.WriteString("|" + strings.Join(cells, " |") + "\n")
	}
	sb.WriteString("|===\n")
}

// escapeCell - экранирует разделитель колонок, чтобы ресурс с "|" не ломал таблицу
func escapeCell(s string) string {
	return strings.ReplaceAll(s, "|", `\|`)
}

// timeRangeValue - первая или последняя запись, "-" если записей нет
func timeRangeValue(r *TimeRange, first bool) string {
	switch {
	case r == nil:
		return "-"
	case first:
		return r.First
	default:
		return r.Last
	}
}

// windowValue - граница окна since/until, "-" если не задана
func windowValue(w *Window, since bool) string {
	if w == nil {
		return "-"
	}
	v := w.Until
	if since {
		v = w.Since
	}
	if v == nil {
		return "-"
	}
	return *v
}

// itoa - целое число в строку
func itoa(n int) string {
	return strconv.Itoa(n)
}

// ftoa - дробное число в строку без лишних нулей
func ftoa(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}
