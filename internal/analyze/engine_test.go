package analyze

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/avequa/incident-radar/internal/source"
)

// TestRun_Streaming - чтение по одному байту, пустые строки и последняя строка без \n
func TestRun_Streaming(t *testing.T) {
	data := testLine + "\n\n" + testLine + "\n   \n" + testLine

	rep, _ := runReader(t, iotest.OneByteReader(strings.NewReader(data)))
	if rep.ProcessedLinesCount != 3 || rep.TotalRequestsCount != 3 {
		t.Fatalf("processed = %d, total = %d, want 3 and 3", rep.ProcessedLinesCount, rep.TotalRequestsCount)
	}
}

// TestRun_BadLine - битая строка пропускается с предупреждением, чтение продолжается
func TestRun_BadLine(t *testing.T) {
	rep, warnings := runReader(t, strings.NewReader(testLine+"\nbroken line\n"+testLine+"\n"))

	if rep.MalformedLinesCount != 1 || rep.TotalRequestsCount != 2 {
		t.Fatalf("malformed = %d, total = %d, want 1 and 2", rep.MalformedLinesCount, rep.TotalRequestsCount)
	}
	if warnings != "WARN: skipped malformed log line (stdin:2)\n" {
		t.Fatalf("warnings = %q", warnings)
	}
}

// lineOfLen - корректная строка лога ровно из n байт
func lineOfLen(t *testing.T, n int) string {
	t.Helper()
	prefix := `93.180.71.3 - - [17/May/2015:08:05:32 +0000] "GET /long HTTP/1.1" 200 10 "-" "`
	pad := n - len(prefix) - 1
	if pad < 0 {
		t.Fatalf("n = %d is too small", n)
	}
	return prefix + strings.Repeat("a", pad) + `"`
}

// TestRun_TooLongLine - строка длиннее MaxLineBytes битая: остаток пропускается, чтение продолжается
func TestRun_TooLongLine(t *testing.T) {
	tests := []struct {
		name          string
		reader        func() io.Reader
		wantProcessed int
		wantMalformed int
		wantTotal     int
	}{
		{
			name: "long line in the middle",
			reader: func() io.Reader {
				return io.MultiReader(
					strings.NewReader(testLine+"\n"),
					&repeatReader{b: 'x', n: 3 * MaxLineBytes},
					strings.NewReader("\n"+testLine+"\n"),
				)
			},
			wantProcessed: 3, wantMalformed: 1, wantTotal: 2,
		},
		{
			name: "long line is the last one without newline",
			reader: func() io.Reader {
				return io.MultiReader(strings.NewReader(testLine+"\n"), &repeatReader{b: 'x', n: MaxLineBytes + 1})
			},
			wantProcessed: 2, wantMalformed: 1, wantTotal: 1,
		},
		{
			name: "two long lines in a row",
			reader: func() io.Reader {
				return io.MultiReader(
					&repeatReader{b: 'x', n: MaxLineBytes + 1}, strings.NewReader("\n"),
					&repeatReader{b: 'y', n: MaxLineBytes + 1}, strings.NewReader("\n"+testLine),
				)
			},
			wantProcessed: 3, wantMalformed: 2, wantTotal: 1,
		},
		{
			name: "valid line of exactly MaxLineBytes with CRLF",
			reader: func() io.Reader {
				return strings.NewReader(lineOfLen(t, MaxLineBytes) + "\r\n" + testLine + "\r\n")
			},
			wantProcessed: 2, wantMalformed: 0, wantTotal: 2,
		},
		{
			name: "line of MaxLineBytes+1 is malformed",
			reader: func() io.Reader {
				return strings.NewReader(lineOfLen(t, MaxLineBytes+1) + "\n" + testLine + "\n")
			},
			wantProcessed: 2, wantMalformed: 1, wantTotal: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rep, warnings := runReader(t, tt.reader())

			if rep.ProcessedLinesCount != tt.wantProcessed || rep.MalformedLinesCount != tt.wantMalformed ||
				rep.TotalRequestsCount != tt.wantTotal {
				t.Fatalf("processed/malformed/total = %d/%d/%d, want %d/%d/%d",
					rep.ProcessedLinesCount, rep.MalformedLinesCount, rep.TotalRequestsCount,
					tt.wantProcessed, tt.wantMalformed, tt.wantTotal)
			}
			if got := strings.Count(warnings, warnBadLine); got != tt.wantMalformed {
				t.Fatalf("warnings = %d, want %d", got, tt.wantMalformed)
			}
		})
	}
}

// TestRun_ReaderError - ошибка чтения посреди потока - ошибка источника, а не битая строка
func TestRun_ReaderError(t *testing.T) {
	r := io.MultiReader(strings.NewReader(testLine+"\n"), iotest.ErrReader(errors.New("connection reset")))
	_, err := Run(context.Background(), Options{Path: StdinPath, Stdin: r})
	if !errors.Is(err, source.ErrSource) || !strings.Contains(err.Error(), "connection reset") {
		t.Fatalf("err = %v, want ErrSource with connection reset", err)
	}
}

// TestRun_Canceled - отмена context останавливает чтение длинного потока
func TestRun_Canceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	lines := strings.Repeat(testLine+"\n", 2*ctxCheckEvery)
	_, err := Run(ctx, Options{Path: StdinPath, Stdin: strings.NewReader(lines)})
	if !errors.Is(err, context.Canceled) || IsUsageError(err) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

// TestRun_Quiet - с QuietWarnings вместо предупреждения на строку одна сводка
func TestRun_Quiet(t *testing.T) {
	_, warnings, err := runStdin(t, "bad\n"+testLine+"\nbad\nbad\n", Options{QuietWarnings: true})
	if err != nil {
		t.Fatal(err)
	}
	if warnings != "WARN: skipped 3 malformed log lines\n" {
		t.Fatalf("warnings = %q", warnings)
	}
}

// TestRun_Window - since включается, until нет; окно сочетается с датами from/to
func TestRun_Window(t *testing.T) {
	data := strings.Join([]string{
		logLine(at(7, 59, 59), "1.1.1.1", "GET", "/a", 200, 1),
		logLine(at(8, 0, 0), "1.1.1.1", "GET", "/a", 200, 1),
		logLine(at(8, 30, 0), "1.1.1.1", "GET", "/a", 200, 1),
		logLine(at(9, 0, 0), "1.1.1.1", "GET", "/a", 200, 1),
		logLine(at(9, 0, 0).AddDate(0, 0, 1), "1.1.1.1", "GET", "/a", 200, 1),
	}, "\n")

	day := time.Date(2015, time.May, 17, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		opts Options
		want int
	}{
		{"no window", Options{}, 5},
		{"since inclusive, until exclusive", Options{Since: new(at(8, 0, 0)), Until: new(at(9, 0, 0))}, 2},
		{"only since", Options{Since: new(at(8, 30, 0))}, 3},
		{"since with date filter", Options{Since: new(at(8, 30, 0)), To: &day}, 2},
		{"timezone offset", Options{Since: new(at(8, 0, 0).In(time.FixedZone("MSK", 3*3600)))}, 4},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rep, _, err := runStdin(t, data, tt.opts)
			if err != nil {
				t.Fatal(err)
			}
			if rep.TotalRequestsCount != tt.want {
				t.Fatalf("TotalRequestsCount = %d, want %d", rep.TotalRequestsCount, tt.want)
			}
			if rep.TimeRange == nil || rep.TimeRange.First != "2015-05-17T07:59:59Z" || rep.TimeRange.Last != "2015-05-18T09:00:00Z" {
				t.Fatalf("TimeRange = %+v, want range of all records", rep.TimeRange)
			}
		})
	}
}

// TestRun_WindowInReport - окно попадает в отчет в RFC3339 UTC, без окна поля нет
func TestRun_WindowInReport(t *testing.T) {
	rep, _, err := runStdin(t, testLine, Options{Since: new(at(8, 0, 0).In(time.FixedZone("MSK", 3*3600)))})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Window == nil || *rep.Window.Since != "2015-05-17T08:00:00Z" || rep.Window.Until != nil {
		t.Fatalf("Window = %+v", rep.Window)
	}

	rep, _, err = runStdin(t, testLine, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Window != nil {
		t.Fatalf("Window = %+v, want nil", rep.Window)
	}
}

// TestRun_Aggregates - классы ответов, доли ошибок, методы, top IP и ресурсы с 5xx за один проход
func TestRun_Aggregates(t *testing.T) {
	data := strings.Join([]string{
		logLine(at(8, 0, 0), "10.0.0.1", "GET", "/", 200, 100),
		logLine(at(8, 0, 1), "10.0.0.1", "GET", "/api", 500, 10),
		logLine(at(8, 0, 2), "10.0.0.2", "POST", "/api", 503, 10),
		logLine(at(8, 0, 3), "10.0.0.2", "POST", "/login", 502, 10),
		logLine(at(8, 0, 4), "10.0.0.3", "GET", "/missing", 404, 0),
		logLine(at(8, 0, 5), "10.0.0.1", "HEAD", "/", 301, 0),
		logLine(at(8, 0, 6), "10.0.0.4", "GET", "/", 101, 0),
		logLine(at(8, 0, 7), "10.0.0.4", "GET", "/", 200, 0),
	}, "\n")

	rep, _, err := runStdin(t, data, Options{Top: 2})
	if err != nil {
		t.Fatal(err)
	}

	if want := (StatusClasses{Informational: 1, Success: 2, Redirection: 1, ClientError: 1, ServerError: 3}); rep.StatusClasses != want {
		t.Fatalf("StatusClasses = %+v, want %+v", rep.StatusClasses, want)
	}
	if rep.ErrorRate != 0.375 || rep.ClientErrorRate != 0.125 {
		t.Fatalf("ErrorRate = %v, ClientErrorRate = %v, want 0.375 and 0.125", rep.ErrorRate, rep.ClientErrorRate)
	}
	if want := []MethodCount{{"GET", 5}, {"POST", 2}, {"HEAD", 1}}; !equal(rep.Methods, want) {
		t.Fatalf("Methods = %v, want %v", rep.Methods, want)
	}
	if want := []IPCount{{"10.0.0.1", 3}, {"10.0.0.2", 2}}; !equal(rep.TopIPs, want) {
		t.Fatalf("TopIPs = %v, want %v", rep.TopIPs, want)
	}
	if want := []ErrorResourceCount{{"/api", 2}, {"/login", 1}}; !equal(rep.TopErrorResources, want) {
		t.Fatalf("TopErrorResources = %v, want %v", rep.TopErrorResources, want)
	}
	if len(rep.Resources) != 2 {
		t.Fatalf("Resources = %v, want top 2", rep.Resources)
	}
	if rep.SchemaVersion != SchemaVersion {
		t.Fatalf("SchemaVersion = %q", rep.SchemaVersion)
	}
}

// TestRun_EmptyAggregates - без записей списки пустые, а не nil, доли ошибок нулевые
func TestRun_EmptyAggregates(t *testing.T) {
	rep, _, err := runStdin(t, "", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Methods == nil || rep.TopIPs == nil || rep.TopErrorResources == nil || rep.ErrorRate != 0 || rep.TimeRange != nil {
		t.Fatalf("report = %+v", rep)
	}
}

// equal - поэлементное сравнение срезов
func equal[T comparable](a, b []T) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestRun_Comparison - одна запись учитывается и в основном, и в сравниваемом периоде за один проход
func TestRun_Comparison(t *testing.T) {
	day2 := strings.Replace(testLine, "17/May/2015", "18/May/2015", 1)
	may17 := time.Date(2015, time.May, 17, 0, 0, 0, 0, time.UTC)
	may18 := time.Date(2015, time.May, 18, 0, 0, 0, 0, time.UTC)

	rep, _, err := runStdin(t, testLine+"\n"+day2+"\n", Options{From: &may18, CompareFrom: &may17, CompareTo: &may18})
	if err != nil {
		t.Fatal(err)
	}
	if rep.ProcessedLinesCount != 2 || rep.TotalRequestsCount != 1 {
		t.Fatalf("processed = %d, total = %d, want 2 and 1", rep.ProcessedLinesCount, rep.TotalRequestsCount)
	}
	if rep.Comparison == nil || rep.Comparison.TotalRequestsCount != 2 || rep.Comparison.Delta.TotalRequestsCount != -1 {
		t.Fatalf("Comparison = %+v", rep.Comparison)
	}
}

// TestRun_InvalidOptions - неверные опции возвращают ErrInvalidOptions до чтения источника
func TestRun_InvalidOptions(t *testing.T) {
	day := time.Date(2015, time.May, 17, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		opts Options
	}{
		{"no path", Options{}},
		{"negative top", Options{Path: "a.log", Top: -1}},
		{"too large top", Options{Path: "a.log", Top: MaxTop + 1}},
		{"bucket below 1s", Options{Path: "a.log", Bucket: 500 * time.Millisecond}},
		{"bucket above 24h", Options{Path: "a.log", Bucket: 25 * time.Hour}},
		{"bucket not whole seconds", Options{Path: "a.log", Bucket: 1500 * time.Millisecond}},
		{"since equals until", Options{Path: "a.log", Since: new(at(8, 0, 0)), Until: new(at(8, 0, 0))}},
		{"to before from", Options{Path: "a.log", From: &day, To: new(day.AddDate(0, 0, -1))}},
		{"compareTo before compareFrom", Options{Path: "a.log", CompareFrom: &day, CompareTo: new(day.AddDate(0, 0, -1))}},
		{"stdin without reader", Options{Path: StdinPath}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Run(context.Background(), tt.opts)
			if !errors.Is(err, ErrInvalidOptions) || !IsUsageError(err) {
				t.Fatalf("err = %v, want ErrInvalidOptions", err)
			}
		})
	}
}
