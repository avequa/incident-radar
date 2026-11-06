// Package analyze - движок анализа логов для CLI и MCP-сервера: опции, один проход по источникам и отчет
package analyze

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/avequa/incident-radar/internal/report"
	"github.com/avequa/incident-radar/internal/stats"
)

// SchemaVersion - версия JSON-контракта отчета, меняется при несовместимых изменениях полей
const SchemaVersion = "1.0.0"

// Ограничения движка
const (
	MaxLineBytes             = 1 << 20
	DefaultMaxTimelinePoints = 10_000
	MaxTop                   = 1000
	MinBucket                = time.Second
	MaxBucket                = 24 * time.Hour
)

// Ожидаемые ошибки движка, все они означают ошибку использования (exit code 2)
var (
	ErrInvalidOptions = errors.New("invalid options")
	ErrForbidden      = errors.New("source is not allowed")
	ErrLimit          = errors.New("limit exceeded")
)

// StdinPath - значение Path для чтения лога из Options.Stdin
const StdinPath = "-"

// Options - параметры одного анализа
type Options struct {
	Path              string
	From              *time.Time
	To                *time.Time
	CompareFrom       *time.Time
	CompareTo         *time.Time
	Since             *time.Time
	Until             *time.Time
	Bucket            time.Duration
	Top               int
	MaxTimelinePoints int
	Stdin             io.Reader
	HTTPClient        *http.Client
	Policy            *Policy
	Warnings          io.Writer
	QuietWarnings     bool
}

// Report - полный отчет: базовый отчет и агрегаты для поиска инцидента
type Report struct {
	SchemaVersion string `json:"schemaVersion" jsonschema:"report contract version (semver)"`
	report.Report
	TimeRange         *TimeRange           `json:"timeRange,omitempty" jsonschema:"time of the first and last valid record in the sources, before any filtering"`
	Window            *Window              `json:"window,omitempty" jsonschema:"applied --since/--until window"`
	StatusClasses     StatusClasses        `json:"statusClasses" jsonschema:"requests per status class in the main period"`
	ErrorRate         float64              `json:"errorRate" jsonschema:"share of 5xx responses in the main period, 0..1"`
	ClientErrorRate   float64              `json:"clientErrorRate" jsonschema:"share of 4xx responses in the main period, 0..1"`
	Methods           []MethodCount        `json:"methods" jsonschema:"requests per HTTP method, count desc"`
	TopIPs            []IPCount            `json:"topIPs" jsonschema:"client IPs with most requests, count desc"`
	TopErrorResources []ErrorResourceCount `json:"topErrorResources" jsonschema:"resources with most 5xx responses, count desc"`
	Timeline          *Timeline            `json:"timeline,omitempty" jsonschema:"requests per time bucket, present only when bucket is set"`
}

// TimeRange - время первой и последней корректной записи в RFC3339 UTC
type TimeRange struct {
	First string `json:"first"`
	Last  string `json:"last"`
}

// Window - границы окна --since (включительно) и --until (не включительно) в RFC3339 UTC
type Window struct {
	Since *string `json:"since"`
	Until *string `json:"until"`
}

// StatusClasses - число ответов по классам кодов
type StatusClasses struct {
	Informational int `json:"1xx"`
	Success       int `json:"2xx"`
	Redirection   int `json:"3xx"`
	ClientError   int `json:"4xx"`
	ServerError   int `json:"5xx"`
}

// MethodCount - HTTP-метод и число запросов
type MethodCount struct {
	Method string `json:"method"`
	Count  int    `json:"count"`
}

// IPCount - IP клиента и число запросов
type IPCount struct {
	IP    string `json:"ip"`
	Count int    `json:"count"`
}

// ErrorResourceCount - ресурс и число ответов 5xx
type ErrorResourceCount struct {
	Resource string `json:"resource"`
	Count    int    `json:"count"`
}

// Timeline - временной ряд с шагом Bucket без пропусков: пустые интервалы идут с нулями
type Timeline struct {
	Bucket string          `json:"bucket" jsonschema:"bucket size, e.g. 1m, 5m, 1h"`
	Points []TimelinePoint `json:"points"`
}

// TimelinePoint - статистика одного интервала временного ряда
type TimelinePoint struct {
	Start    string `json:"start" jsonschema:"bucket start, RFC3339 UTC"`
	Requests int    `json:"requests"`
	StatusClasses
	ErrorRate    float64 `json:"errorRate" jsonschema:"share of 5xx in the bucket, 0..1"`
	P95SizeBytes int     `json:"p95SizeBytes" jsonschema:"95th percentile of response size in the bucket"`
}

// Run - читает все источники за один проход и собирает отчет
func Run(ctx context.Context, opts Options) (Report, error) {
	if err := opts.normalize(); err != nil {
		return Report{}, err
	}

	sources, err := resolveSources(opts)
	if err != nil {
		return Report{}, err
	}

	e := newEngine(opts)
	client := opts.Policy.restrictClient(opts.HTTPClient)
	budget := newBudget(opts.Policy)

	for _, src := range sources {
		if err := e.readSource(ctx, client, budget, src); err != nil {
			return Report{}, err
		}
	}

	if opts.QuietWarnings && e.lines.malformed > 0 {
		_, _ = fmt.Fprintf(opts.Warnings, "WARN: skipped %d malformed log lines\n", e.lines.malformed)
	}

	return e.report(sources)
}

// normalize - проверяет опции и подставляет значения по умолчанию
func (o *Options) normalize() error {
	if o.Path == "" {
		return fmt.Errorf("%w: path is required", ErrInvalidOptions)
	}
	if o.Top == 0 {
		o.Top = stats.DefaultTop
	}
	if o.Top < 1 || o.Top > MaxTop {
		return fmt.Errorf("%w: top must be between 1 and %d", ErrInvalidOptions, MaxTop)
	}
	if o.Bucket != 0 && (o.Bucket < MinBucket || o.Bucket > MaxBucket || o.Bucket%time.Second != 0) {
		return fmt.Errorf("%w: bucket must be a whole number of seconds between %s and %s", ErrInvalidOptions, MinBucket, MaxBucket)
	}
	if err := checkOrder("from", "to", o.From, o.To, true); err != nil {
		return err
	}
	if err := checkOrder("compareFrom", "compareTo", o.CompareFrom, o.CompareTo, true); err != nil {
		return err
	}
	if err := checkOrder("since", "until", o.Since, o.Until, false); err != nil {
		return err
	}
	if o.MaxTimelinePoints <= 0 {
		o.MaxTimelinePoints = DefaultMaxTimelinePoints
	}
	if o.HTTPClient == nil {
		o.HTTPClient = DefaultHTTPClient()
	}
	if o.Warnings == nil {
		o.Warnings = io.Discard
	}
	return nil
}

// checkOrder - если заданы обе границы, начало должно быть раньше конца (или равно, если allowEqual)
func checkOrder(fromName, toName string, from, to *time.Time, allowEqual bool) error {
	if from == nil || to == nil {
		return nil
	}
	if to.Before(*from) || (!allowEqual && to.Equal(*from)) {
		return fmt.Errorf("%w: %s must be before %s", ErrInvalidOptions, fromName, toName)
	}
	return nil
}

// DefaultHTTPClient - HTTP-клиент с таймаутами на соединение и заголовки, но без общего таймаута на тело,
// чтобы большой лог успевал дочитаться; отменой чтения управляет context
func DefaultHTTPClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
		},
	}
}
