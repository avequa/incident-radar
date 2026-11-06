package analyze

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/avequa/incident-radar/internal/parser"
	"github.com/avequa/incident-radar/internal/stats"
)

// errorRateDigits - точность долей ошибок
const errorRateDigits = 4

// incidentCollector - агрегаты основного периода для поиска инцидента: классы, методы, IP, ресурсы с 5xx
type incidentCollector struct {
	total          int
	classes        StatusClasses
	methods        map[string]int
	ips            map[string]int
	errorResources map[string]int
}

// newIncidentCollector - создает пустой incidentCollector
func newIncidentCollector() *incidentCollector {
	return &incidentCollector{
		methods:        make(map[string]int),
		ips:            make(map[string]int),
		errorResources: make(map[string]int),
	}
}

// add - учитывает одну запись основного периода
func (c *incidentCollector) add(rec parser.LogRecord) {
	c.total++
	c.classes.add(rec.Status)
	c.methods[rec.Method]++
	c.ips[rec.IP]++
	if rec.Status >= 500 {
		c.errorResources[rec.Resource]++
	}
}

// fill - переносит агрегаты в отчет, top-списки ограничивает размером top
func (c *incidentCollector) fill(rep *Report, top int) {
	rep.StatusClasses = c.classes
	rep.ErrorRate = share(c.classes.ServerError, c.total)
	rep.ClientErrorRate = share(c.classes.ClientError, c.total)

	rep.Methods = convert(topCounts(c.methods, len(c.methods)), func(n nameCount) MethodCount {
		return MethodCount{Method: n.name, Count: n.count}
	})
	rep.TopIPs = convert(topCounts(c.ips, top), func(n nameCount) IPCount {
		return IPCount{IP: n.name, Count: n.count}
	})
	rep.TopErrorResources = convert(topCounts(c.errorResources, top), func(n nameCount) ErrorResourceCount {
		return ErrorResourceCount{Resource: n.name, Count: n.count}
	})
}

// add - учитывает код ответа в своем классе
func (s *StatusClasses) add(status int) {
	switch status / 100 {
	case 1:
		s.Informational++
	case 2:
		s.Success++
	case 3:
		s.Redirection++
	case 4:
		s.ClientError++
	case 5:
		s.ServerError++
	}
}

// share - доля part от total с округлением, 0 для пустого total
func share(part, total int) float64 {
	if total == 0 {
		return 0
	}
	return stats.Round(float64(part)/float64(total), errorRateDigits)
}

// nameCount - имя и счетчик для top-списков
type nameCount struct {
	name  string
	count int
}

// topCounts - n записей с наибольшим счетчиком: по убыванию, при равенстве по имени
func topCounts(m map[string]int, n int) []nameCount {
	out := make([]nameCount, 0, len(m))
	for name, count := range m {
		out = append(out, nameCount{name: name, count: count})
	}
	slices.SortFunc(out, func(a, b nameCount) int {
		if c := cmp.Compare(b.count, a.count); c != 0 {
			return c
		}
		return cmp.Compare(a.name, b.name)
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// convert - преобразует срез, всегда возвращает не-nil срез (в JSON [] вместо null)
func convert[T any](in []nameCount, f func(nameCount) T) []T {
	out := make([]T, 0, len(in))
	for _, v := range in {
		out = append(out, f(v))
	}
	return out
}

// timelineCollector - копит статистику по интервалам фиксированной длины
type timelineCollector struct {
	step   time.Duration
	points map[int64]*pointAcc
}

// pointAcc - накопитель одного интервала
type pointAcc struct {
	requests int
	classes  StatusClasses
	sizes    []int
}

// newTimelineCollector - создает сборщик временного ряда с шагом step
func newTimelineCollector(step time.Duration) *timelineCollector {
	return &timelineCollector{step: step, points: make(map[int64]*pointAcc)}
}

// add - учитывает запись в ее интервале (начало интервала выровнено по UTC)
func (t *timelineCollector) add(rec parser.LogRecord) {
	key := rec.Time.UTC().Truncate(t.step).Unix()
	acc := t.points[key]
	if acc == nil {
		acc = &pointAcc{}
		t.points[key] = acc
	}
	acc.requests++
	acc.classes.add(rec.Status)
	acc.sizes = append(acc.sizes, rec.Size)
}

// build - собирает ряд от первого до последнего интервала, заполняя пропуски нулями
func (t *timelineCollector) build(maxPoints int) (*Timeline, error) {
	timeline := &Timeline{Bucket: formatBucket(t.step), Points: []TimelinePoint{}}
	if len(t.points) == 0 {
		return timeline, nil
	}

	keys := slices.Sorted(maps.Keys(t.points))
	stepSec := int64(t.step / time.Second)
	first, last := keys[0], keys[len(keys)-1]

	count := (last-first)/stepSec + 1
	if count > int64(maxPoints) {
		return nil, fmt.Errorf("%w: timeline would have %d points (max %d): use a larger bucket or a narrower since/until window",
			ErrLimit, count, maxPoints)
	}

	timeline.Points = make([]TimelinePoint, 0, count)
	for key := first; key <= last; key += stepSec {
		point := TimelinePoint{Start: formatTime(time.Unix(key, 0))}
		if acc := t.points[key]; acc != nil {
			point.Requests = acc.requests
			point.StatusClasses = acc.classes
			point.ErrorRate = share(acc.classes.ServerError, acc.requests)
			point.P95SizeBytes = stats.P95(acc.sizes)
		}
		timeline.Points = append(timeline.Points, point)
	}
	return timeline, nil
}

// formatBucket - короткая запись шага: 1m, 5m, 1h, 90s
func formatBucket(d time.Duration) string {
	switch {
	case d%time.Hour == 0:
		return fmt.Sprintf("%dh", d/time.Hour)
	case d%time.Minute == 0:
		return fmt.Sprintf("%dm", d/time.Minute)
	default:
		return fmt.Sprintf("%ds", d/time.Second)
	}
}
