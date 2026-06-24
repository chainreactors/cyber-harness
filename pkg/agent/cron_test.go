package agent

import (
	"testing"
	"time"
)

func TestParseCron(t *testing.T) {
	tests := []struct {
		expr    string
		wantErr bool
	}{
		{"*/5 * * * *", false},
		{"0 */2 * * *", false},
		{"30 9 * * 1-5", false},
		{"0 0 1 * *", false},
		{"0,30 * * * *", false},
		{"0-15/3 * * * *", false},
		{"* * * * *", false},
		{"bad", true},
		{"* * *", true},
		{"60 * * * *", true},
		{"* 25 * * *", true},
	}
	for _, tt := range tests {
		_, err := ParseCron(tt.expr)
		if (err != nil) != tt.wantErr {
			t.Errorf("ParseCron(%q): err=%v, wantErr=%v", tt.expr, err, tt.wantErr)
		}
	}
}

func TestCronNext(t *testing.T) {
	cron, err := ParseCron("*/5 * * * *")
	if err != nil {
		t.Fatal(err)
	}

	base := time.Date(2026, 6, 23, 10, 7, 30, 0, time.UTC)
	next := cron.Next(base)
	want := time.Date(2026, 6, 23, 10, 10, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Errorf("Next(%v) = %v, want %v", base, next, want)
	}
}

func TestCronNextHourly(t *testing.T) {
	cron, err := ParseCron("0 */2 * * *")
	if err != nil {
		t.Fatal(err)
	}

	base := time.Date(2026, 6, 23, 11, 0, 0, 0, time.UTC)
	next := cron.Next(base)
	want := time.Date(2026, 6, 23, 12, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Errorf("Next(%v) = %v, want %v", base, next, want)
	}
}

func TestCronNextWeekday(t *testing.T) {
	// 30 9 * * 1-5 = 9:30 on Mon-Fri
	cron, err := ParseCron("30 9 * * 1-5")
	if err != nil {
		t.Fatal(err)
	}

	// 2026-06-23 is a Tuesday
	base := time.Date(2026, 6, 23, 9, 30, 0, 0, time.UTC)
	next := cron.Next(base)
	// should be next day (Wednesday) at 9:30
	want := time.Date(2026, 6, 24, 9, 30, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Errorf("Next(%v) = %v, want %v", base, next, want)
	}
}
