package mcpserver

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/avequa/incident-radar/internal/analyze"
)

// bearerTransport - добавляет заголовок Authorization к каждому запросу
type bearerTransport struct {
	token string
}

// RoundTrip - отправляет запрос с Bearer-токеном
func (b bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

// newHTTPServer - тестовый HTTP-сервер с MCP-обработчиком
func newHTTPServer(t *testing.T, token string) *httptest.Server {
	t.Helper()
	server, err := New(Config{Policy: &analyze.Policy{Root: newRoot(t)}})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(NewHTTPHandler(server, token, slog.New(slog.NewTextHandler(io.Discard, nil))))
	t.Cleanup(ts.Close)
	return ts
}

// TestHTTP_ToolCall - клиент SDK вызывает analyze_logs по streamable HTTP с токеном
func TestHTTP_ToolCall(t *testing.T) {
	ts := newHTTPServer(t, "secret")

	transport := &mcp.StreamableClientTransport{
		Endpoint:   ts.URL + MCPPath,
		HTTPClient: &http.Client{Transport: bearerTransport{token: "secret"}},
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "http-client", Version: "test"}, nil)
	session, err := client.Connect(context.Background(), transport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()

	var rep analyze.Report
	res := call(t, session, "analyze_logs", map[string]any{"path": "access.log"}, &rep)
	if res.IsError || rep.TotalRequestsCount != 18 {
		t.Fatalf("result = %+v, total = %d", res, rep.TotalRequestsCount)
	}
}

// TestHTTP_Auth - без токена или с неверным токеном /mcp отвечает 401, /healthz доступен всегда
func TestHTTP_Auth(t *testing.T) {
	ts := newHTTPServer(t, "secret")

	tests := []struct {
		name   string
		path   string
		header string
		want   int
	}{
		{"no token", MCPPath, "", http.StatusUnauthorized},
		{"wrong token", MCPPath, "Bearer wrong", http.StatusUnauthorized},
		{"healthz without token", "/healthz", "", http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, ts.URL+tt.path,
				strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
			if err != nil {
				t.Fatal(err)
			}
			if tt.path == "/healthz" {
				req.Method = http.MethodGet
			}
			if tt.header != "" {
				req.Header.Set("Authorization", tt.header)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			_ = resp.Body.Close()
			if resp.StatusCode != tt.want {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tt.want)
			}
		})
	}
}

// TestServe_GracefulShutdown - отмена context останавливает HTTP-сервер без ошибки
func TestServe_GracefulShutdown(t *testing.T) {
	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	go func() { done <- serve(ctx, listener, http.NotFoundHandler(), logger) }()

	url := "http://" + listener.Addr().String() + "/"
	for i := 0; ; i++ {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			break
		}
		if i == 50 {
			t.Fatalf("server did not start: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not stop")
	}
}

// TestServeHTTP_BadAddr - занятый или неверный адрес возвращает ошибку сразу
func TestServeHTTP_BadAddr(t *testing.T) {
	err := ServeHTTP(context.Background(), "256.0.0.1:99999", http.NotFoundHandler(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err == nil {
		t.Fatal("expected listen error")
	}
}
