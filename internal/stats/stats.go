// Package stats считает статистику по записям лога и фильтрует их по периоду
package stats

import (
	"maps"
	"math"
	"slices"
	"sort"
	"time"

	"github.com/avequa/incident-radar/internal/parser"
)

// DefaultTop - размер top-списка ресурсов по умолчанию
const DefaultTop = 10

// HandlerLogLine - копит статистику по записям лога за один проход
type HandlerLogLine struct {
	total     int
	codes     map[int]int
	resources map[string]int
	sizes     []int
	protocols map[string]bool
	weekdays  [7]int
}

// NewHandlerLogLine - создает пустой HandlerLogLine
func NewHandlerLogLine() *HandlerLogLine {
	return &HandlerLogLine{
		codes:     make(map[int]int),
		resources: make(map[string]int),
		protocols: make(map[string]bool),
	}
}

// Add - учитывает одну запись лога
func (h *HandlerLogLine) Add(log parser.LogRecord) {
	h.total++
	h.codes[log.Status]++
	h.resources[log.Resource]++
	h.sizes = append(h.sizes, log.Size)
	h.protocols[log.Protocol] = true
	h.weekdays[log.Time.Weekday()]++
}

// ResponseSizeStats - размер ответа в байтах: среднее, максимум и p95
type ResponseSizeStats struct {
	Average float64 `json:"average" jsonschema:"mean response size, rounded to 2 decimals"`
	Max     int     `json:"max" jsonschema:"maximum response size"`
	P95     int     `json:"p95" jsonschema:"95th percentile of response size (nearest-rank)"`
}

// ResourceCount - ресурс и число запросов к нему
type ResourceCount struct {
	Resource string `json:"resource"`
	Count    int    `json:"totalRequestsCount"`
}

// WeekdayCount - день недели и число запросов в этот день
type WeekdayCount struct {
	Weekday string `json:"weekday"`
	Count   int    `json:"weekdayCount"`
}

// Result - итоговая статистика по отфильтрованным записям
type Result struct {
	TotalRequestsCount  int               `json:"totalRequestsCount" jsonschema:"number of requests in the main period"`
	ResponseCodes       map[int]int       `json:"responseCodes" jsonschema:"HTTP status code -> number of requests"`
	Resources           []ResourceCount   `json:"resources" jsonschema:"most requested resources, count desc, then resource asc"`
	ResponseSizeInBytes ResponseSizeStats `json:"responseSizeInBytes"`
	Protocols           []string          `json:"protocols" jsonschema:"unique protocols, sorted"`
	RequestsByWeekday   []WeekdayCount    `json:"requestsByWeekday" jsonschema:"requests per weekday, Monday first"`
}

// Result - собирает статистику с top-10 ресурсов
func (h *HandlerLogLine) Result() Result {
	return h.ResultTop(DefaultTop)
}

// ResultTop - собирает статистику с top-n ресурсов
func (h *HandlerLogLine) ResultTop(n int) Result {
	return Result{
		TotalRequestsCount: h.total,
		ResponseCodes:      h.codes,
		Resources:          topResources(h.resources, n),
		ResponseSizeInBytes: ResponseSizeStats{
			Average: average(h.sizes),
			Max:     maxSize(h.sizes),
			P95:     P95(h.sizes),
		},
		Protocols:         uniqueProtocol(h.protocols),
		RequestsByWeekday: weekdayCounts(h.weekdays),
	}
}

// uniqueProtocol - отсортированный список протоколов, пустой срез вместо nil
func uniqueProtocol(protocols map[string]bool) []string {
	sorted := slices.Sorted(maps.Keys(protocols))
	if sorted == nil {
		return []string{}
	}
	return sorted
}

// topResources - n самых запрашиваемых ресурсов: по убыванию числа запросов, при равенстве по алфавиту
func topResources(resources map[string]int, n int) []ResourceCount {
	out := make([]ResourceCount, 0, len(resources))
	for resource, count := range resources {
		out = append(out, ResourceCount{Resource: resource, Count: count})
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Resource < out[j].Resource
	})

	if len(out) > n {
		return out[:n]
	}
	return out
}

// weekdayCounts - число запросов по дням недели, начиная с понедельника
func weekdayCounts(days [7]int) []WeekdayCount {
	order := []time.Weekday{
		time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday, time.Saturday, time.Sunday,
	}

	out := make([]WeekdayCount, 0, len(order))
	for _, day := range order {
		out = append(out, WeekdayCount{Weekday: day.String(), Count: days[day]})
	}
	return out
}

// P95 - 95-й перцентиль методом nearest-rank: элемент с однобазовым индексом ceil(0.95 * N)
func P95(sizes []int) int {
	if len(sizes) == 0 {
		return 0
	}

	sorted := slices.Clone(sizes)
	slices.Sort(sorted)

	rank := int(math.Ceil(0.95 * float64(len(sorted))))
	return sorted[rank-1]
}

// average - средний размер ответа, округленный до двух знаков
func average(sizes []int) float64 {
	if len(sizes) == 0 {
		return 0
	}

	sum := 0
	for _, value := range sizes {
		sum += value
	}
	return Round(float64(sum)/float64(len(sizes)), 2)
}

// Round - округляет x до digits знаков после запятой
func Round(x float64, digits int) float64 {
	pow := math.Pow(10, float64(digits))
	return math.Round(x*pow) / pow
}

// maxSize - максимальный размер ответа
func maxSize(sizes []int) int {
	maxValue := 0
	for _, size := range sizes {
		maxValue = max(maxValue, size)
	}
	return maxValue
}
