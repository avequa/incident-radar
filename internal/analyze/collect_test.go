package analyze

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// TestTimeline - интервалы выровнены по UTC, пропуски заполнены нулями, errorRate и p95 по интервалу
func TestTimeline(t *testing.T) {
	msk := time.FixedZone("MSK", 3*3600)
	data := strings.Join([]string{
		logLine(at(8, 0, 10), "1.1.1.1", "GET", "/", 200, 100),
		logLine(at(8, 4, 59), "1.1.1.1", "GET", "/", 200, 300),
		logLine(at(8, 15, 0).In(msk), "1.1.1.1", "GET", "/api", 500, 10),
		logLine(at(8, 16, 0), "1.1.1.1", "GET", "/api", 502, 20),
		logLine(at(8, 17, 0), "1.1.1.1", "GET", "/", 404, 0),
		logLine(at(8, 18, 0), "1.1.1.1", "GET", "/", 301, 0),
	}, "\n")

	rep, _, err := runStdin(t, data, Options{Bucket: 5 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}

	want := []TimelinePoint{
		{Start: "2015-05-17T08:00:00Z", Requests: 2, StatusClasses: StatusClasses{Success: 2}, P95SizeBytes: 300},
		{Start: "2015-05-17T08:05:00Z"},
		{Start: "2015-05-17T08:10:00Z"},
		{
			Start: "2015-05-17T08:15:00Z", Requests: 4,
			StatusClasses: StatusClasses{Redirection: 1, ClientError: 1, ServerError: 2},
			ErrorRate:     0.5, P95SizeBytes: 20,
		},
	}
	if rep.Timeline == nil || rep.Timeline.Bucket != "5m" || !equal(rep.Timeline.Points, want) {
		t.Fatalf("Timeline = %+v, want %+v", rep.Timeline, want)
	}
}

// TestTimeline_OnlyMainWindow - во временной ряд попадают только записи окна since/until
func TestTimeline_OnlyMainWindow(t *testing.T) {
	data := logLine(at(8, 0, 0), "1.1.1.1", "GET", "/", 200, 1) + "\n" + logLine(at(9, 0, 0), "1.1.1.1", "GET", "/", 200, 1)

	rep, _, err := runStdin(t, data, Options{Bucket: time.Minute, Since: new(at(8, 30, 0))})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Timeline.Points) != 1 || rep.Timeline.Points[0].Start != "2015-05-17T09:00:00Z" {
		t.Fatalf("Points = %+v", rep.Timeline.Points)
	}
}

// TestTimeline_Empty - без записей временной ряд пустой, но присутствует
func TestTimeline_Empty(t *testing.T) {
	rep, _, err := runStdin(t, "", Options{Bucket: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Timeline == nil || rep.Timeline.Bucket != "1h" || rep.Timeline.Points == nil || len(rep.Timeline.Points) != 0 {
		t.Fatalf("Timeline = %+v", rep.Timeline)
	}
}

// TestTimeline_TooManyPoints - слишком длинный ряд возвращает ErrLimit
func TestTimeline_TooManyPoints(t *testing.T) {
	data := logLine(at(0, 0, 0), "1.1.1.1", "GET", "/", 200, 1) + "\n" + logLine(at(23, 0, 0), "1.1.1.1", "GET", "/", 200, 1)

	_, _, err := runStdin(t, data, Options{Bucket: time.Minute, MaxTimelinePoints: 100})
	if !errors.Is(err, ErrLimit) {
		t.Fatalf("err = %v, want ErrLimit", err)
	}
}

// TestFormatBucket - короткая запись шага временного ряда
func TestFormatBucket(t *testing.T) {
	tests := map[time.Duration]string{
		time.Minute:      "1m",
		5 * time.Minute:  "5m",
		time.Hour:        "1h",
		90 * time.Second: "90s",
		24 * time.Hour:   "24h",
	}
	for d, want := range tests {
		if got := formatBucket(d); got != want {
			t.Fatalf("formatBucket(%v) = %q, want %q", d, got, want)
		}
	}
}
