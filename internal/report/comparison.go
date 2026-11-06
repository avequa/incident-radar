package report

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/avequa/incident-radar/internal/stats"
)

// Period - границы периода в отчете в формате YYYY-MM-DD, null - граница не задана
type Period struct {
	From *string `json:"from"`
	To   *string `json:"to"`
}

// Comparison - статистика сравниваемого периода и разница с основным (delta = основной - сравниваемый)
type Comparison struct {
	CurrentPeriod       Period                  `json:"currentPeriod"`
	ComparedPeriod      Period                  `json:"comparedPeriod"`
	TotalRequestsCount  int                     `json:"totalRequestsCount"`
	ResponseSizeInBytes stats.ResponseSizeStats `json:"responseSizeInBytes"`
	ResponseCodes       map[int]int             `json:"responseCodes"`
	Delta               Delta                   `json:"delta"`
}

// Delta - разница "основной период - сравниваемый"
type Delta struct {
	TotalRequestsCount int         `json:"totalRequestsCount"`
	Average            float64     `json:"average"`
	Max                int         `json:"max"`
	P95                int         `json:"p95"`
	ResponseCodes      map[int]int `json:"responseCodes"`
}

// NewComparison - собирает сравнение основного и сравниваемого периодов
func NewComparison(currentPeriod, comparedPeriod stats.Period, current, compared stats.Result) *Comparison {
	return &Comparison{
		CurrentPeriod:       newPeriod(currentPeriod),
		ComparedPeriod:      newPeriod(comparedPeriod),
		TotalRequestsCount:  compared.TotalRequestsCount,
		ResponseSizeInBytes: compared.ResponseSizeInBytes,
		ResponseCodes:       compared.ResponseCodes,
		Delta: Delta{
			TotalRequestsCount: current.TotalRequestsCount - compared.TotalRequestsCount,
			Average:            stats.Round(current.ResponseSizeInBytes.Average-compared.ResponseSizeInBytes.Average, 2),
			Max:                current.ResponseSizeInBytes.Max - compared.ResponseSizeInBytes.Max,
			P95:                current.ResponseSizeInBytes.P95 - compared.ResponseSizeInBytes.P95,
			ResponseCodes:      codesDelta(current.ResponseCodes, compared.ResponseCodes),
		},
	}
}

// codesDelta - разница по каждому коду, отсутствующий в периоде код считается за 0
func codesDelta(current, compared map[int]int) map[int]int {
	delta := make(map[int]int)
	for code, count := range current {
		delta[code] += count
	}
	for code, count := range compared {
		delta[code] -= count
	}
	return delta
}

// newPeriod - переводит stats.Period в вид для отчета
func newPeriod(p stats.Period) Period {
	return Period{From: formatDate(p.From), To: formatDate(p.To)}
}

// formatDate - дата в формате YYYY-MM-DD, nil для незаданной границы
func formatDate(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.Format("2006-01-02")
	return &s
}

// periodText - период для таблиц: "2025-03-01 - 2025-03-02", открытая граница - "..."
func periodText(p Period) string {
	left, right := "...", "..."
	if p.From != nil {
		left = *p.From
	}
	if p.To != nil {
		right = *p.To
	}
	return left + " - " + right
}

// comparisonRow - строка таблицы сравнения, общая для Markdown и AsciiDoc
type comparisonRow struct {
	metric   string
	current  string
	compared string
	delta    string
}

// comparisonRows - строки таблицы сравнения: запросы, размеры и каждый код ответа
func comparisonRows(rep Report) []comparisonRow {
	c := rep.Comparison
	cur := rep.ResponseSizeInBytes
	cmp := c.ResponseSizeInBytes

	rows := []comparisonRow{
		{"Количество запросов", fmt.Sprint(rep.TotalRequestsCount), fmt.Sprint(c.TotalRequestsCount), fmt.Sprintf("%+d", c.Delta.TotalRequestsCount)},
		{"Средний размер ответа", fmt.Sprint(cur.Average), fmt.Sprint(cmp.Average), fmt.Sprintf("%+g", c.Delta.Average)},
		{"Максимальный размер ответа", fmt.Sprint(cur.Max), fmt.Sprint(cmp.Max), fmt.Sprintf("%+d", c.Delta.Max)},
		{"p95 размера ответа", fmt.Sprint(cur.P95), fmt.Sprint(cmp.P95), fmt.Sprintf("%+d", c.Delta.P95)},
	}

	for _, code := range slices.Sorted(maps.Keys(c.Delta.ResponseCodes)) {
		rows = append(rows, comparisonRow{
			metric:   "Код " + StatusName(code),
			current:  fmt.Sprint(rep.ResponseCodes[code]),
			compared: fmt.Sprint(c.ResponseCodes[code]),
			delta:    fmt.Sprintf("%+d", c.Delta.ResponseCodes[code]),
		})
	}
	return rows
}

// writeComparisonMarkdown - таблица "Сравнение периодов" в Markdown, если сравнение есть
func writeComparisonMarkdown(sb *strings.Builder, rep Report) {
	if rep.Comparison == nil {
		return
	}

	sb.WriteString("\n## Сравнение периодов\n\n")
	sb.WriteString("| Метрика | Основной | Сравниваемый | Разница |\n")
	sb.WriteString("|:---|---:|---:|---:|\n")
	fmt.Fprintf(sb, "| Период | %s | %s | |\n", periodText(rep.Comparison.CurrentPeriod), periodText(rep.Comparison.ComparedPeriod))
	for _, row := range comparisonRows(rep) {
		fmt.Fprintf(sb, "| %s | %s | %s | %s |\n", row.metric, row.current, row.compared, row.delta)
	}
}

// writeComparisonAdoc - таблица "Сравнение периодов" в AsciiDoc, если сравнение есть
func writeComparisonAdoc(sb *strings.Builder, rep Report) {
	if rep.Comparison == nil {
		return
	}

	sb.WriteString("\n== Сравнение периодов\n\n")
	sb.WriteString("[cols=\"<,>,>,>\",options=\"header\"]\n|===\n")
	sb.WriteString("|Метрика |Основной |Сравниваемый |Разница\n")
	fmt.Fprintf(sb, "|Период |%s |%s |\n", periodText(rep.Comparison.CurrentPeriod), periodText(rep.Comparison.ComparedPeriod))
	for _, row := range comparisonRows(rep) {
		fmt.Fprintf(sb, "|%s |%s |%s |%s\n", row.metric, row.current, row.compared, row.delta)
	}
	sb.WriteString("|===\n")
}
