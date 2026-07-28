package agent

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// CronExpr represents a parsed 5-field cron expression:
//
//	minute(0-59) hour(0-23) day(1-31) month(1-12) weekday(0-6, 0=Sun)
type CronExpr struct {
	Minute  []bool // [0..59]
	Hour    []bool // [0..23]
	Day     []bool // [1..31]
	Month   []bool // [1..12]
	Weekday []bool // [0..6]
	Raw     string
}

// ParseCron parses a standard 5-field cron expression.
//
// Supports: literal, *(any), */step, range(a-b), range/step(a-b/step), list(a,b,c).
func ParseCron(expr string) (*CronExpr, error) {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return nil, fmt.Errorf("cron: expected 5 fields, got %d", len(fields))
	}

	minute, err := parseField(fields[0], 0, 59)
	if err != nil {
		return nil, fmt.Errorf("cron minute: %w", err)
	}
	hour, err := parseField(fields[1], 0, 23)
	if err != nil {
		return nil, fmt.Errorf("cron hour: %w", err)
	}
	day, err := parseField(fields[2], 1, 31)
	if err != nil {
		return nil, fmt.Errorf("cron day: %w", err)
	}
	month, err := parseField(fields[3], 1, 12)
	if err != nil {
		return nil, fmt.Errorf("cron month: %w", err)
	}
	weekday, err := parseField(fields[4], 0, 6)
	if err != nil {
		return nil, fmt.Errorf("cron weekday: %w", err)
	}

	return &CronExpr{
		Minute:  minute,
		Hour:    hour,
		Day:     day,
		Month:   month,
		Weekday: weekday,
		Raw:     expr,
	}, nil
}

// Next returns the next fire time strictly after t.
func (c *CronExpr) Next(t time.Time) time.Time {
	t = t.Add(time.Minute).Truncate(time.Minute)

	// cap search at 4 years to avoid infinite loop on impossible expressions
	limit := t.Add(4 * 365 * 24 * time.Hour)
	for t.Before(limit) {
		if !c.Month[t.Month()] {
			// skip to next month
			t = time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, t.Location())
			continue
		}
		if !c.Day[t.Day()] || !c.Weekday[int(t.Weekday())] {
			t = t.AddDate(0, 0, 1)
			t = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
			continue
		}
		if !c.Hour[t.Hour()] {
			t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour()+1, 0, 0, 0, t.Location())
			continue
		}
		if !c.Minute[t.Minute()] {
			t = t.Add(time.Minute)
			continue
		}
		return t
	}
	return time.Time{}
}

func (c *CronExpr) String() string { return c.Raw }

// parseField parses one cron field into a bool slice indexed [0..max].
func parseField(field string, min, max int) ([]bool, error) {
	set := make([]bool, max+1)
	for _, part := range strings.Split(field, ",") {
		if err := parsePart(part, min, max, set); err != nil {
			return nil, err
		}
	}
	return set, nil
}

func parsePart(part string, min, max int, set []bool) error {
	// */step
	if strings.HasPrefix(part, "*/") {
		step, err := strconv.Atoi(part[2:])
		if err != nil || step <= 0 {
			return fmt.Errorf("invalid step %q", part)
		}
		for i := min; i <= max; i += step {
			set[i] = true
		}
		return nil
	}

	// *
	if part == "*" {
		for i := min; i <= max; i++ {
			set[i] = true
		}
		return nil
	}

	// range or range/step
	if strings.Contains(part, "-") {
		rangePart, stepStr := part, ""
		if idx := strings.Index(part, "/"); idx != -1 {
			rangePart, stepStr = part[:idx], part[idx+1:]
		}
		bounds := strings.SplitN(rangePart, "-", 2)
		lo, err := strconv.Atoi(bounds[0])
		if err != nil {
			return fmt.Errorf("invalid range %q", part)
		}
		hi, err := strconv.Atoi(bounds[1])
		if err != nil {
			return fmt.Errorf("invalid range %q", part)
		}
		if lo < min || hi > max || lo > hi {
			return fmt.Errorf("range %d-%d out of bounds [%d,%d]", lo, hi, min, max)
		}
		step := 1
		if stepStr != "" {
			step, err = strconv.Atoi(stepStr)
			if err != nil || step <= 0 {
				return fmt.Errorf("invalid step in %q", part)
			}
		}
		for i := lo; i <= hi; i += step {
			set[i] = true
		}
		return nil
	}

	// literal
	n, err := strconv.Atoi(part)
	if err != nil {
		return fmt.Errorf("invalid value %q", part)
	}
	if n < min || n > max {
		return fmt.Errorf("value %d out of bounds [%d,%d]", n, min, max)
	}
	set[n] = true
	return nil
}
