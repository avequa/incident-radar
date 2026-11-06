package stats

import "time"

// Period - период по датам из --from и --to, обе границы включительно, nil - граница не задана
type Period struct {
	From *time.Time
	To   *time.Time
}

// FilterPeriodEveryLine - попадает ли дата записи в период
func (p Period) FilterPeriodEveryLine(t time.Time) bool {
	year, month, day := t.Date()
	date := time.Date(year, month, day, 0, 0, 0, 0, time.UTC)

	if p.From != nil && date.Before(*p.From) {
		return false
	}
	if p.To != nil && date.After(*p.To) {
		return false
	}
	return true
}
