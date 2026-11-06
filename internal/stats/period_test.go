package stats

import (
	"testing"
	"time"
)

// TestPeriod - фильтр работает с одной, двумя и без границ, обе границы включаются целиком
func TestPeriod(t *testing.T) {
	from := time.Date(2025, time.March, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2025, time.March, 3, 0, 0, 0, 0, time.UTC)

	fromDayStart := time.Date(2025, time.March, 1, 0, 0, 0, 0, time.UTC)
	toDayEnd := time.Date(2025, time.March, 3, 23, 59, 59, 0, time.UTC)
	dayBefore := time.Date(2025, time.February, 28, 12, 0, 0, 0, time.UTC)
	dayAfter := time.Date(2025, time.March, 4, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name   string
		period Period
		t      time.Time
		want   bool
	}{
		{"no bounds", Period{}, dayBefore, true},

		{"only from: from day included", Period{From: &from}, fromDayStart, true},
		{"only from: day before skipped", Period{From: &from}, dayBefore, false},

		{"only to: to day included", Period{To: &to}, toDayEnd, true},
		{"only to: day after skipped", Period{To: &to}, dayAfter, false},

		{"both: from day included", Period{From: &from, To: &to}, fromDayStart, true},
		{"both: to day included", Period{From: &from, To: &to}, toDayEnd, true},
		{"both: day after skipped", Period{From: &from, To: &to}, dayAfter, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.period.FilterPeriodEveryLine(tt.t)
			if got != tt.want {
				t.Fatalf("FilterPeriodEveryLine(%v) = %v, want %v", tt.t, got, tt.want)
			}
		})
	}
}
