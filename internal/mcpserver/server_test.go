package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/avequa/incident-radar/internal/analyze"
)

// logLine - корректная строка лога с заданными временем, кодом и ресурсом
func logLine(t time.Time, status int, resource string) string {
	return fmt.Sprintf(`10.0.0.1 - - [%s] "GET %s HTTP/1.1" %d 100 "-" "test"`, t.Format("02/Jan/2006:15:04:05 -0700"), resource, status)
}

// incidentLog - лог, в котором с 08:03 начинаются ответы 5xx на /api
func incidentLog() string {
	start := time.Date(2015, time.May, 17, 8, 0, 0, 0, time.UTC)
	var lines []string
	for i := range 6 {
		t := start.Add(time.Duration(i) * time.Minute)
		lines = append(lines, logLine(t, 200, "/"), logLine(t.Add(time.Second), 200, "/api"))
		if i >= 3 {
			lines = append(lines, logLine(t.Add(2*time.Second), 503, "/api"), logLine(t.Add(3*time.Second), 500, "/api"))
		}
	}
	return strings.Join(lines, "\n") + "\n"
}

// newRoot - временная папка с логами для политики сервера
func newRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for name, content := range map[string]string{
		"access.log":      incidentLog(),
		"nginx/error.txt": logLine(time.Date(2015, time.May, 17, 9, 0, 0, 0, time.UTC), 502, "/x") + "\n",
		"README.md":       "not a log",
	} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// connect - поднимает сервер и клиента на in-memory транспорте SDK
func connect(t *testing.T, cfg Config) *mcp.ClientSession {
	t.Helper()
	server, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "test"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

// call - вызывает tool и раскладывает structuredContent в out
func call(t *testing.T, session *mcp.ClientSession, name string, args map[string]any, out any) *mcp.CallToolResult {
	t.Helper()
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%s): %v", name, err)
	}
	if out != nil && !res.IsError {
		data, err := json.Marshal(res.StructuredContent)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, out); err != nil {
			t.Fatalf("unmarshal structured content: %v", err)
		}
	}
	return res
}

// errorText - текст ошибки из результата tool
func errorText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	if !res.IsError || len(res.Content) == 0 {
		t.Fatalf("expected tool error, got %+v", res)
	}
	text, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("content is %T, want text", res.Content[0])
	}
	return text.Text
}

// TestListTools - сервер объявляет оба tools с входной и выходной схемами и read-only аннотациями
func TestListTools(t *testing.T) {
	session := connect(t, Config{Policy: &analyze.Policy{Root: newRoot(t)}})

	res, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
		if tool.InputSchema == nil || tool.OutputSchema == nil || tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Fatalf("tool %s has no schemas or read-only hint: %+v", tool.Name, tool)
		}
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"analyze_logs", "list_sources"}) {
		t.Fatalf("tools = %v", names)
	}

	if init := session.InitializeResult(); init == nil || !strings.Contains(init.Instructions, "list_sources") {
		t.Fatalf("server instructions are missing: %+v", init)
	}
}

// TestAnalyzeLogs - analyze_logs возвращает отчет по схеме, timeline показывает начало инцидента
func TestAnalyzeLogs(t *testing.T) {
	session := connect(t, Config{Policy: &analyze.Policy{Root: newRoot(t)}})

	var rep analyze.Report
	res := call(t, session, "analyze_logs", map[string]any{
		"path": "access.log", "bucket": "1m", "since": "2015-05-17T08:00:00Z", "until": "2015-05-17T09:00:00Z", "top": 3,
	}, &rep)
	if res.IsError {
		t.Fatalf("tool error: %s", errorText(t, res))
	}

	if rep.SchemaVersion != analyze.SchemaVersion || rep.TotalRequestsCount != 18 || rep.StatusClasses.ServerError != 6 {
		t.Fatalf("report = %+v", rep)
	}
	if len(rep.Files) != 1 || rep.Files[0] != "access.log" {
		t.Fatalf("Files = %v, want relative path", rep.Files)
	}
	if len(rep.TopErrorResources) != 1 || rep.TopErrorResources[0].Resource != "/api" {
		t.Fatalf("TopErrorResources = %+v", rep.TopErrorResources)
	}

	onset := ""
	for _, p := range rep.Timeline.Points {
		if p.ErrorRate > 0 {
			onset = p.Start
			break
		}
	}
	if onset != "2015-05-17T08:03:00Z" {
		t.Fatalf("incident onset = %q, want 2015-05-17T08:03:00Z; timeline = %+v", onset, rep.Timeline.Points)
	}

	if len(res.Content) == 0 {
		t.Fatal("no text content for clients without structuredContent support")
	}
	if text, ok := res.Content[0].(*mcp.TextContent); !ok || !json.Valid([]byte(text.Text)) {
		t.Fatalf("text content is not JSON: %+v", res.Content[0])
	}
}

// TestAnalyzeLogs_Errors - ошибки аргументов, политики и лимитов возвращаются как tool error с понятным текстом
func TestAnalyzeLogs_Errors(t *testing.T) {
	root := newRoot(t)
	session := connect(t, Config{Policy: &analyze.Policy{Root: root, AllowStdin: false}, MaxTimelinePoints: 3})

	tests := []struct {
		name string
		args map[string]any
		want string
	}{
		{"missing path", map[string]any{}, "path"},
		{"outside root", map[string]any{"path": "../../etc/passwd.log"}, "outside the allowed directories"},
		{"stdin", map[string]any{"path": "-"}, "stdin is disabled"},
		{"url not allowed", map[string]any{"path": "http://169.254.169.254/latest.log"}, "does not match any allowed prefix"},
		{"bad since", map[string]any{"path": "access.log", "since": "yesterday"}, "invalid since"},
		{"bad date", map[string]any{"path": "access.log", "from": "17.05.2015"}, "from"},
		{"bad bucket", map[string]any{"path": "access.log", "bucket": "2m"}, "bucket"},
		{"top too large", map[string]any{"path": "access.log", "top": 1000}, "top"},
		{"timeline too long", map[string]any{"path": "access.log", "bucket": "1m"}, "timeline would have 6 points"},
		{"no files", map[string]any{"path": "*.csv"}, "no .log or .txt files"},
		{"missing file inside root", map[string]any{"path": "nope.log"}, "no such file"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := call(t, session, "analyze_logs", tt.args, nil)
			if text := errorText(t, res); !strings.Contains(text, tt.want) {
				t.Fatalf("error = %q, want it to contain %q", text, tt.want)
			}
		})
	}
}

// TestAnalyzeLogs_Timeout - долгий анализ прерывается по CallTimeout с подсказкой сузить окно
func TestAnalyzeLogs_Timeout(t *testing.T) {
	session := connect(t, Config{Policy: &analyze.Policy{Root: newRoot(t)}, CallTimeout: time.Nanosecond})

	res := call(t, session, "analyze_logs", map[string]any{"path": "access.log"}, nil)
	if text := errorText(t, res); !strings.Contains(text, "timed out") {
		t.Fatalf("error = %q, want timeout", text)
	}
}

// TestListSourcesTool - list_sources находит только логи внутри root, пути относительные
func TestListSourcesTool(t *testing.T) {
	session := connect(t, Config{Policy: &analyze.Policy{Root: newRoot(t)}})

	var all analyze.SourceList
	call(t, session, "list_sources", map[string]any{}, &all)
	if len(all.Sources) != 1 || all.Sources[0].Path != "access.log" || all.Sources[0].SizeBytes == 0 {
		t.Fatalf("sources = %+v", all.Sources)
	}

	var nested analyze.SourceList
	call(t, session, "list_sources", map[string]any{"glob": "nginx/*"}, &nested)
	if len(nested.Sources) != 1 || nested.Sources[0].Path != "nginx/error.txt" {
		t.Fatalf("nested sources = %+v", nested.Sources)
	}

	res := call(t, session, "list_sources", map[string]any{"glob": "https://example.com/*.log"}, nil)
	if text := errorText(t, res); !strings.Contains(text, "only local files") {
		t.Fatalf("error = %q", text)
	}
}

// TestAcquire_Busy - при занятых слотах вызов ждет и отказывает по отмене context
func TestAcquire_Busy(t *testing.T) {
	h := &handlers{sem: make(chan struct{}, 1)}
	release, err := h.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := h.acquire(ctx); err == nil || !strings.Contains(err.Error(), "busy") {
		t.Fatalf("err = %v, want busy", err)
	}
}
