// Package mcpserver - MCP-сервер incident-radar: tools analyze_logs и list_sources поверх движка analyze
package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/avequa/incident-radar/internal/analyze"
)

// Значения конфигурации по умолчанию
const (
	DefaultCallTimeout       = 60 * time.Second
	DefaultMaxConcurrent     = 4
	DefaultMaxTimelinePoints = 300
	maxTop                   = 100
)

// instructions - подсказка агенту, как пользоваться инструментами
const instructions = `incident-radar analyzes NGINX access logs (combined format).
Workflow: call list_sources to find log files, then analyze_logs without bucket to get totals,
errorRate and timeRange. To find when an incident started, call analyze_logs again with
since/until around the suspicious period and bucket=1m or 5m, and look for the first timeline
point where errorRate or 5xx jumps. topErrorResources and topIPs show what failed and who called it.`

// Config - настройки MCP-сервера
type Config struct {
	Version           string
	Policy            *analyze.Policy
	HTTPClient        *http.Client
	CallTimeout       time.Duration
	MaxConcurrent     int
	MaxTimelinePoints int
	Logger            *slog.Logger
}

// AnalyzeInput - аргументы tool analyze_logs
type AnalyzeInput struct {
	Path        string `json:"path" jsonschema:"log file, glob pattern (e.g. *.log) or http(s) URL; relative paths are resolved against the server root"`
	From        string `json:"from,omitempty" jsonschema:"start date YYYY-MM-DD, inclusive"`
	To          string `json:"to,omitempty" jsonschema:"end date YYYY-MM-DD, inclusive"`
	Since       string `json:"since,omitempty" jsonschema:"start time RFC3339, inclusive, e.g. 2015-05-17T08:00:00Z"`
	Until       string `json:"until,omitempty" jsonschema:"end time RFC3339, exclusive"`
	Bucket      string `json:"bucket,omitempty" jsonschema:"timeline step; omit to skip the timeline"`
	CompareFrom string `json:"compareFrom,omitempty" jsonschema:"start date YYYY-MM-DD of a period to compare with"`
	CompareTo   string `json:"compareTo,omitempty" jsonschema:"end date YYYY-MM-DD of a period to compare with"`
	Top         int    `json:"top,omitempty" jsonschema:"size of top lists (resources, IPs, 5xx resources), default 10"`
}

// ListSourcesInput - аргументы tool list_sources
type ListSourcesInput struct {
	Glob string `json:"glob,omitempty" jsonschema:"glob pattern relative to the server root, e.g. *.log or nginx/*.log; default *"`
}

// handlers - обработчики tools с общими настройками и ограничением параллелизма
type handlers struct {
	cfg Config
	sem chan struct{}
}

// New - создает MCP-сервер с tools analyze_logs и list_sources
func New(cfg Config) (*mcp.Server, error) {
	cfg = withDefaults(cfg)

	reportSchema, err := ReportSchema()
	if err != nil {
		return nil, fmt.Errorf("report schema: %w", err)
	}
	inputSchema, err := analyzeInputSchema()
	if err != nil {
		return nil, fmt.Errorf("analyze_logs input schema: %w", err)
	}

	server := mcp.NewServer(
		&mcp.Implementation{Name: "incident-radar", Title: "Incident Radar", Version: cfg.Version},
		&mcp.ServerOptions{Instructions: instructions, Logger: cfg.Logger},
	)
	h := &handlers{cfg: cfg, sem: make(chan struct{}, cfg.MaxConcurrent)}

	mcp.AddTool(server, &mcp.Tool{
		Name:  "analyze_logs",
		Title: "Analyze NGINX logs",
		Description: "Reads NGINX access logs in one streaming pass and returns JSON statistics: totals, status codes " +
			"and classes, errorRate (5xx share), top resources, top IPs, resources with 5xx, methods, response sizes " +
			"(average/max/p95), time range of the logs and, with bucket, a gap-free timeline to find when an incident started.",
		InputSchema:  inputSchema,
		OutputSchema: reportSchema,
		Annotations:  &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: new(false)},
	}, h.analyzeLogs)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_sources",
		Title:       "List log files",
		Description: "Lists local .log and .txt files that analyze_logs is allowed to read, with size and modification time.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: new(false)},
	}, h.listSources)

	return server, nil
}

// withDefaults - подставляет значения по умолчанию в незаданные поля
func withDefaults(cfg Config) Config {
	if cfg.Version == "" {
		cfg.Version = "dev"
	}
	if cfg.Policy == nil {
		cfg.Policy = &analyze.Policy{}
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = analyze.DefaultHTTPClient()
	}
	if cfg.CallTimeout <= 0 {
		cfg.CallTimeout = DefaultCallTimeout
	}
	if cfg.MaxConcurrent <= 0 {
		cfg.MaxConcurrent = DefaultMaxConcurrent
	}
	if cfg.MaxTimelinePoints <= 0 {
		cfg.MaxTimelinePoints = DefaultMaxTimelinePoints
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return cfg
}

// analyzeLogs - обработчик analyze_logs: проверка аргументов, лимит параллелизма, таймаут, анализ
func (h *handlers) analyzeLogs(ctx context.Context, _ *mcp.CallToolRequest, in AnalyzeInput) (*mcp.CallToolResult, analyze.Report, error) {
	start := time.Now()
	rep, err := h.runAnalyze(ctx, in)
	h.logCall(ctx, "analyze_logs", start, err,
		slog.String("path", in.Path), slog.String("bucket", in.Bucket), slog.Int("requests", rep.TotalRequestsCount))
	return nil, rep, err
}

// runAnalyze - выполняет анализ под политикой сервера
func (h *handlers) runAnalyze(ctx context.Context, in AnalyzeInput) (analyze.Report, error) {
	opts, err := in.options()
	if err != nil {
		return analyze.Report{}, err
	}

	release, err := h.acquire(ctx)
	if err != nil {
		return analyze.Report{}, err
	}
	defer release()

	ctx, cancel := context.WithTimeout(ctx, h.cfg.CallTimeout)
	defer cancel()

	opts.Policy = h.cfg.Policy
	opts.HTTPClient = h.cfg.HTTPClient
	opts.MaxTimelinePoints = h.cfg.MaxTimelinePoints
	opts.QuietWarnings = true

	rep, err := analyze.Run(ctx, opts)
	if errors.Is(err, context.DeadlineExceeded) {
		return analyze.Report{}, fmt.Errorf("analysis timed out after %s: use a narrower since/until window or fewer files", h.cfg.CallTimeout)
	}
	return rep, err
}

// listSources - обработчик list_sources
func (h *handlers) listSources(ctx context.Context, _ *mcp.CallToolRequest, in ListSourcesInput) (*mcp.CallToolResult, analyze.SourceList, error) {
	start := time.Now()
	list, err := analyze.ListSources(in.Glob, h.cfg.Policy)
	h.logCall(ctx, "list_sources", start, err, slog.String("glob", in.Glob), slog.Int("sources", len(list.Sources)))
	return nil, list, err
}

// acquire - занимает слот параллелизма или ждет его до отмены context
func (h *handlers) acquire(ctx context.Context) (func(), error) {
	select {
	case h.sem <- struct{}{}:
		return func() { <-h.sem }, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("server is busy: %w", ctx.Err())
	}
}

// logCall - пишет в лог результат вызова tool: длительность, ошибка и класс ошибки
func (h *handlers) logCall(ctx context.Context, tool string, start time.Time, err error, attrs ...slog.Attr) {
	attrs = append(attrs, slog.String("tool", tool), durationMs(start))
	level := slog.LevelInfo
	if err != nil {
		level = slog.LevelWarn
		attrs = append(attrs, slog.String("error", err.Error()), slog.Bool("usageError", analyze.IsUsageError(err)))
	}
	h.cfg.Logger.LogAttrs(ctx, level, "tool call", attrs...)
}

// options - разбирает строковые аргументы tool в опции движка
func (in AnalyzeInput) options() (analyze.Options, error) {
	opts := analyze.Options{Path: in.Path, Top: in.Top}
	if in.Top > maxTop {
		return opts, fmt.Errorf("%w: top must be between 1 and %d", analyze.ErrInvalidOptions, maxTop)
	}

	var err error
	dates := []struct {
		name  string
		value string
		dst   **time.Time
	}{
		{"from", in.From, &opts.From},
		{"to", in.To, &opts.To},
		{"compareFrom", in.CompareFrom, &opts.CompareFrom},
		{"compareTo", in.CompareTo, &opts.CompareTo},
	}
	for _, d := range dates {
		if *d.dst, err = parseTime(d.name, d.value, "2006-01-02", "YYYY-MM-DD"); err != nil {
			return opts, err
		}
	}
	if opts.Since, err = parseTime("since", in.Since, time.RFC3339, "RFC3339, e.g. 2015-05-17T08:00:00Z"); err != nil {
		return opts, err
	}
	if opts.Until, err = parseTime("until", in.Until, time.RFC3339, "RFC3339, e.g. 2015-05-17T09:00:00Z"); err != nil {
		return opts, err
	}

	if in.Bucket != "" {
		if opts.Bucket, err = time.ParseDuration(in.Bucket); err != nil {
			return opts, fmt.Errorf("%w: invalid bucket %q (use 1m, 5m, 15m or 1h)", analyze.ErrInvalidOptions, in.Bucket)
		}
	}
	return opts, nil
}

// parseTime - разбирает значение по layout, пустая строка - nil
func parseTime(name, value, layout, hint string) (*time.Time, error) {
	if value == "" {
		return nil, nil
	}
	t, err := time.Parse(layout, value)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid %s %q (use %s)", analyze.ErrInvalidOptions, name, value, hint)
	}
	return &t, nil
}

// durationMs - длительность от start в миллисекундах для логов
func durationMs(start time.Time) slog.Attr {
	return slog.Float64("durationMs", float64(time.Since(start).Microseconds())/1000)
}
