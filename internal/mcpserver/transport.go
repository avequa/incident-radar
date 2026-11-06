package mcpserver

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Таймауты HTTP-сервера
const (
	readHeaderTimeout = 10 * time.Second
	idleTimeout       = 2 * time.Minute
	sessionTimeout    = 30 * time.Minute
	shutdownTimeout   = 10 * time.Second
)

// MCPPath - путь streamable HTTP endpoint
const MCPPath = "/mcp"

// ServeStdio - обслуживает одного клиента через stdin/stdout до закрытия stdin или отмены context
func ServeStdio(ctx context.Context, server *mcp.Server, logger *slog.Logger) error {
	logger.Info("serving MCP over stdio")
	err := server.Run(ctx, &mcp.StdioTransport{})
	if err != nil && ctx.Err() == nil {
		return fmt.Errorf("stdio transport: %w", err)
	}
	logger.Info("stdio transport stopped")
	return nil
}

// NewHTTPHandler - HTTP-обработчик: streamable MCP на /mcp (с Bearer-токеном, если он задан) и /healthz
func NewHTTPHandler(server *mcp.Server, token string, logger *slog.Logger) http.Handler {
	mcpHandler := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return server },
		&mcp.StreamableHTTPOptions{Logger: logger, SessionTimeout: sessionTimeout},
	)

	mux := http.NewServeMux()
	mux.Handle(MCPPath, requireToken(token, http.NewCrossOriginProtection().Handler(mcpHandler)))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})
	return accessLog(logger, mux)
}

// ServeHTTP - слушает addr до отмены context, затем корректно завершает соединения
func ServeHTTP(ctx context.Context, addr string, handler http.Handler, logger *slog.Logger) error {
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}
	return serve(ctx, listener, handler, logger)
}

// serve - обслуживает listener, по отмене context ждет активные запросы не дольше shutdownTimeout
func serve(ctx context.Context, listener net.Listener, handler http.Handler, logger *slog.Logger) error {
	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
		BaseContext:       func(net.Listener) context.Context { return context.WithoutCancel(ctx) },
	}

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(listener) }()
	logger.Info("serving MCP over streamable HTTP", slog.String("addr", listener.Addr().String()), slog.String("path", MCPPath))

	select {
	case err := <-errCh:
		return fmt.Errorf("http server: %w", err)
	case <-ctx.Done():
	}

	logger.Info("shutting down HTTP server")
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Warn("graceful shutdown timed out, closing connections", slog.String("error", err.Error()))
		_ = srv.Close()
	}
	if err := <-errCh; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("http server: %w", err)
	}
	logger.Info("HTTP server stopped")
	return nil
}

// requireToken - пропускает запрос только с заголовком Authorization: Bearer <token>; пустой token - без проверки
func requireToken(token string, next http.Handler) http.Handler {
	if token == "" {
		return next
	}
	want := []byte("Bearer " + token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := []byte(r.Header.Get("Authorization"))
		if subtle.ConstantTimeCompare(got, want) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="incident-radar"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// statusRecorder - запоминает код ответа для access-лога
type statusRecorder struct {
	http.ResponseWriter
	status int
}

// WriteHeader - запоминает код ответа
func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

// Unwrap - дает http.ResponseController доступ к Flush исходного writer (нужно для SSE)
func (r *statusRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

// Flush - пробрасывает Flush исходного writer, иначе потоковые ответы SSE не уходят клиенту
func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// accessLog - пишет метод, путь, код и длительность каждого HTTP-запроса
func accessLog(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		level := slog.LevelDebug
		if rec.status >= 400 {
			level = slog.LevelWarn
		}
		logger.LogAttrs(r.Context(), level, "http request",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", rec.status),
			durationMs(start),
			slog.String("remote", remoteHost(r.RemoteAddr)),
		)
	})
}

// remoteHost - адрес клиента без порта
func remoteHost(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}
