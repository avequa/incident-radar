package stats

import (
	"fmt"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/avequa/incident-radar/internal/parser"
)

// TestResponseCodes - коды 200, 200, 404, 500, 500, 500 группируются как 200: 2, 404: 1, 500: 3
func TestResponseCodes(t *testing.T) {
	h := NewHandlerLogLine()
	for _, code := range []int{200, 200, 404, 500, 500, 500} {
		h.Add(parser.LogRecord{Status: code})
	}

	got := h.Result().ResponseCodes
	want := map[int]int{200: 2, 404: 1, 500: 3}
	if !maps.Equal(got, want) {
		t.Fatalf("ResponseCodes = %v, want %v", got, want)
	}
}

// TestTopResources - ресурсы сортируются по убыванию количества, результат ограничен десятью строками
func TestTopResources(t *testing.T) {
	h := NewHandlerLogLine()
	for i := 1; i <= 12; i++ {
		for range 13 - i {
			h.Add(parser.LogRecord{Resource: fmt.Sprintf("/r%02d", i)})
		}
	}

	got := h.Result().Resources
	if len(got) != 10 {
		t.Fatalf("len = %d, want 10", len(got))
	}
	if got[0] != (ResourceCount{"/r01", 12}) || got[9] != (ResourceCount{"/r10", 3}) {
		t.Fatalf("first = %v, last = %v, want {/r01 12} and {/r10 3}", got[0], got[9])
	}
}

// TestTopResources_EqualCount - при равном числе запросов ресурсы сортируются по имени
func TestTopResources_EqualCount(t *testing.T) {
	h := NewHandlerLogLine()
	for _, r := range []string{"/c", "/a", "/b", "/a", "/b", "/c"} {
		h.Add(parser.LogRecord{Resource: r})
	}

	got := h.Result().Resources
	want := []ResourceCount{{"/a", 2}, {"/b", 2}, {"/c", 2}}
	if !slices.Equal(got, want) {
		t.Fatalf("Resources = %v, want %v", got, want)
	}
}

// TestResponseSize - для размеров 100, 200, 300, 400, 500 average 300, max 500, p95 500
func TestResponseSize(t *testing.T) {
	h := NewHandlerLogLine()
	for _, size := range []int{100, 200, 300, 400, 500} {
		h.Add(parser.LogRecord{Size: size})
	}

	got := h.Result()
	if got.ResponseSizeInBytes.Average != 300 {
		t.Fatalf("Average = %v, want 300", got.ResponseSizeInBytes.Average)
	}
	if got.ResponseSizeInBytes.Max != 500 {
		t.Fatalf("Max = %d, want 500", got.ResponseSizeInBytes.Max)
	}
	if got.ResponseSizeInBytes.P95 != 500 {
		t.Fatalf("P95 = %d, want 500", got.ResponseSizeInBytes.P95)
	}
}

// TestProtocols - уникальные протоколы без повторов, по алфавиту
func TestProtocols(t *testing.T) {
	h := NewHandlerLogLine()
	for _, p := range []string{"HTTP/2.0", "HTTP/1.1", "HTTP/2.0", "HTTP/1.0", "HTTP/1.1"} {
		h.Add(parser.LogRecord{Protocol: p})
	}

	got := h.Result().Protocols
	want := []string{"HTTP/1.0", "HTTP/1.1", "HTTP/2.0"}
	if !slices.Equal(got, want) {
		t.Fatalf("Protocols = %v, want %v", got, want)
	}
}

// TestProtocols_Empty - без записей протоколы - пустой срез, а не nil
func TestProtocols_Empty(t *testing.T) {
	got := NewHandlerLogLine().Result().Protocols
	if got == nil || len(got) != 0 {
		t.Fatalf("Protocols = %#v, want empty slice", got)
	}
}

// TestRequestsByWeekday - все 7 дней недели с понедельника, включая нулевые
func TestRequestsByWeekday(t *testing.T) {
	h := NewHandlerLogLine()
	monday := time.Date(2025, time.March, 3, 10, 0, 0, 0, time.UTC)
	sunday := time.Date(2025, time.March, 9, 10, 0, 0, 0, time.UTC)
	for _, day := range []time.Time{monday, monday, sunday} {
		h.Add(parser.LogRecord{Time: day})
	}

	got := h.Result().RequestsByWeekday
	want := []WeekdayCount{
		{"Monday", 2},
		{"Tuesday", 0},
		{"Wednesday", 0},
		{"Thursday", 0},
		{"Friday", 0},
		{"Saturday", 0},
		{"Sunday", 1},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("RequestsByWeekday = %v, want %v", got, want)
	}
}

// TestResultTop - ResultTop ограничивает список ресурсов заданным размером
func TestResultTop(t *testing.T) {
	h := NewHandlerLogLine()
	for _, r := range []string{"/a", "/a", "/b", "/c"} {
		h.Add(parser.LogRecord{Resource: r})
	}

	got := h.ResultTop(2).Resources
	want := []ResourceCount{{"/a", 2}, {"/b", 1}}
	if !slices.Equal(got, want) {
		t.Fatalf("Resources = %v, want %v", got, want)
	}
}

// TestP95 - p95 методом nearest-rank для пустого, одного и нескольких значений
func TestP95(t *testing.T) {
	tests := []struct {
		name  string
		sizes []int
		want  int
	}{
		{"empty", nil, 0},
		{"single", []int{7}, 7},
		{"unsorted", []int{500, 100, 400, 200, 300}, 500},
		{"twenty values", []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20}, 19},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := P95(tt.sizes); got != tt.want {
				t.Fatalf("P95(%v) = %d, want %d", tt.sizes, got, tt.want)
			}
		})
	}
}
