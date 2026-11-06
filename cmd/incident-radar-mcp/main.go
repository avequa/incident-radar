// Package main - MCP-сервер incident-radar для LLM-агентов (stdio или streamable HTTP)
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/avequa/incident-radar/internal/analyze"
	"github.com/avequa/incident-radar/internal/mcpserver"
)

// version - версия сервера, задается при сборке: -ldflags "-X main.version=1.2.3"
var version = "dev"

// tokenEnv - переменная окружения с Bearer-токеном для HTTP-транспорта
const tokenEnv = "INCIDENT_RADAR_MCP_TOKEN"

// Транспорты сервера
const (
	transportStdio = "stdio"
	transportHTTP  = "http"
)

// config - разобранные флаги запуска
type config struct {
	transport         string
	addr              string
	root              string
	allowDirs         listFlag
	allowURLs         listFlag
	allowStdin        bool
	maxBytes          int64
	maxFiles          int
	callTimeout       time.Duration
	maxConcurrent     int
	maxTimelinePoints int
	logLevel          slog.Level
	logFormat         string
}

// listFlag - флаг, который можно указать несколько раз
type listFlag []string

// String - значения через запятую
func (l *listFlag) String() string {
	return strings.Join(*l, ",")
}

// Set - добавляет значение
func (l *listFlag) Set(v string) error {
	*l = append(*l, v)
	return nil
}

// main - запускает сервер, SIGINT и SIGTERM завершают его корректно
func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Getenv(tokenEnv), os.Stderr)
	stop()
	os.Exit(code)
}

// run - разбирает флаги и обслуживает выбранный транспорт; логи пишутся в stderr, stdout занят протоколом
func run(ctx context.Context, args []string, token string, stderr io.Writer) int {
	cfg, err := parseFlags(args, stderr)
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
		return 2
	}

	logger := newLogger(stderr, cfg)
	policy := &analyze.Policy{
		Root:               cfg.root,
		AllowedDirs:        cfg.allowDirs,
		AllowedURLPrefixes: cfg.allowURLs,
		AllowStdin:         cfg.allowStdin,
		MaxBytes:           cfg.maxBytes,
		MaxFiles:           cfg.maxFiles,
	}

	server, err := mcpserver.New(mcpserver.Config{
		Version:           version,
		Policy:            policy,
		CallTimeout:       cfg.callTimeout,
		MaxConcurrent:     cfg.maxConcurrent,
		MaxTimelinePoints: cfg.maxTimelinePoints,
		Logger:            logger,
	})
	if err != nil {
		logger.Error("cannot create server", slog.String("error", err.Error()))
		return 1
	}

	logger.Info("incident-radar MCP server",
		slog.String("version", version),
		slog.String("transport", cfg.transport),
		slog.String("root", cfg.root),
		slog.Any("allowDirs", []string(cfg.allowDirs)),
		slog.Any("allowURLs", []string(cfg.allowURLs)),
		slog.Int64("maxBytes", cfg.maxBytes),
		slog.Duration("callTimeout", cfg.callTimeout),
	)

	if cfg.transport == transportStdio {
		err = mcpserver.ServeStdio(ctx, server, logger)
	} else {
		if token == "" && !isLoopback(cfg.addr) {
			logger.Warn("HTTP transport listens on a non-loopback address without " + tokenEnv)
		}
		err = mcpserver.ServeHTTP(ctx, cfg.addr, mcpserver.NewHTTPHandler(server, token, logger), logger)
	}
	if err != nil {
		logger.Error("server stopped with error", slog.String("error", err.Error()))
		return 1
	}
	return 0
}

// parseFlags - разбирает и проверяет флаги запуска
func parseFlags(args []string, stderr io.Writer) (config, error) {
	fs := flag.NewFlagSet("incident-radar-mcp", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var cfg config
	var logLevel string
	fs.StringVar(&cfg.transport, "transport", transportStdio, "stdio or http")
	fs.StringVar(&cfg.addr, "addr", "127.0.0.1:8765", "listen address for --transport http")
	fs.StringVar(&cfg.root, "root", ".", "log directory; relative tool paths are resolved against it")
	fs.Var(&cfg.allowDirs, "allow-dir", "extra directory with logs (repeatable)")
	fs.Var(&cfg.allowURLs, "allow-url", "allowed URL prefix, e.g. http://logs.internal/nginx/ (repeatable)")
	fs.BoolVar(&cfg.allowStdin, "allow-stdin", false, "allow path - (only useful for embedding, not for stdio transport)")
	fs.Int64Var(&cfg.maxBytes, "max-bytes", 512<<20, "max bytes read per tool call, 0 = unlimited")
	fs.IntVar(&cfg.maxFiles, "max-files", 100, "max files per tool call")
	fs.DurationVar(&cfg.callTimeout, "call-timeout", mcpserver.DefaultCallTimeout, "timeout of one tool call")
	fs.IntVar(&cfg.maxConcurrent, "max-concurrent", mcpserver.DefaultMaxConcurrent, "max parallel tool calls")
	fs.IntVar(&cfg.maxTimelinePoints, "max-timeline-points", mcpserver.DefaultMaxTimelinePoints,
		"max timeline points per report (keeps the answer within an LLM context)")
	fs.StringVar(&logLevel, "log-level", "info", "debug, info, warn or error")
	fs.StringVar(&cfg.logFormat, "log-format", "json", "json or text")

	if err := fs.Parse(args); err != nil {
		return config{}, err
	}
	if fs.NArg() > 0 {
		return config{}, fmt.Errorf("unexpected arguments %q", fs.Args())
	}
	if cfg.transport != transportStdio && cfg.transport != transportHTTP {
		return config{}, fmt.Errorf("invalid --transport %q (use stdio or http)", cfg.transport)
	}
	if cfg.logFormat != "json" && cfg.logFormat != "text" {
		return config{}, fmt.Errorf("invalid --log-format %q (use json or text)", cfg.logFormat)
	}
	if err := cfg.logLevel.UnmarshalText([]byte(logLevel)); err != nil {
		return config{}, fmt.Errorf("invalid --log-level %q", logLevel)
	}
	if cfg.callTimeout <= 0 || cfg.maxConcurrent <= 0 || cfg.maxFiles <= 0 || cfg.maxTimelinePoints <= 0 || cfg.maxBytes < 0 {
		return config{}, errors.New("limits must be positive")
	}

	root, err := existingDir(cfg.root)
	if err != nil {
		return config{}, fmt.Errorf("invalid --root: %w", err)
	}
	cfg.root = root
	for i, dir := range cfg.allowDirs {
		if cfg.allowDirs[i], err = existingDir(dir); err != nil {
			return config{}, fmt.Errorf("invalid --allow-dir: %w", err)
		}
	}
	for _, prefix := range cfg.allowURLs {
		if !strings.HasPrefix(prefix, "http://") && !strings.HasPrefix(prefix, "https://") {
			return config{}, fmt.Errorf("invalid --allow-url %q: must start with http:// or https://", prefix)
		}
	}
	return cfg, nil
}

// existingDir - абсолютный путь к существующему каталогу
func existingDir(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%q is not a directory", dir)
	}
	return abs, nil
}

// newLogger - slog-логгер в stderr в формате JSON или text
func newLogger(w io.Writer, cfg config) *slog.Logger {
	opts := &slog.HandlerOptions{Level: cfg.logLevel}
	if cfg.logFormat == "text" {
		return slog.New(slog.NewTextHandler(w, opts))
	}
	return slog.New(slog.NewJSONHandler(w, opts))
}

// isLoopback - слушает ли адрес только локальный интерфейс
func isLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
