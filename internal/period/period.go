package period

import (
	"fmt"
	"time"
)

// GenerateWeekPeriods returns the last n ISO week period strings (e.g., "2025W37").
// It counts backwards from the current week.
func GenerateWeekPeriods(n int) []string {
	now := time.Now().UTC()
	periods := make([]string, 0, n)

	for i := n; i >= 1; i-- {
		d := now.AddDate(0, 0, -i*7)
		year, week := d.ISOWeek()
		periods = append(periods, fmt.Sprintf("%dW%02d", year, week))
	}

	return periods
}
