package scheduler

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// CronExpr represents a parsed cron expression
type CronExpr struct {
	Minutes     []int // 0-59
	Hours       []int // 0-23
	DaysOfMonth []int // 1-31
	Months      []int // 1-12
	DaysOfWeek  []int // 0-6 (0=Sunday)
}

// ParseCron parses a 5-field cron expression
// Format: minute hour day-of-month month day-of-week
func ParseCron(expr string) (*CronExpr, error) {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return nil, fmt.Errorf("expected 5 fields, got %d", len(fields))
	}

	minutes, err := parseField(fields[0], 0, 59)
	if err != nil {
		return nil, fmt.Errorf("minute field: %w", err)
	}

	hours, err := parseField(fields[1], 0, 23)
	if err != nil {
		return nil, fmt.Errorf("hour field: %w", err)
	}

	daysOfMonth, err := parseField(fields[2], 1, 31)
	if err != nil {
		return nil, fmt.Errorf("day-of-month field: %w", err)
	}

	months, err := parseField(fields[3], 1, 12)
	if err != nil {
		return nil, fmt.Errorf("month field: %w", err)
	}

	daysOfWeek, err := parseField(fields[4], 0, 6)
	if err != nil {
		return nil, fmt.Errorf("day-of-week field: %w", err)
	}

	return &CronExpr{
		Minutes:     minutes,
		Hours:       hours,
		DaysOfMonth: daysOfMonth,
		Months:      months,
		DaysOfWeek:  daysOfWeek,
	}, nil
}

// parseField parses a single cron field
// Supports: *, */n, n, n-m, n,m,o
func parseField(field string, min, max int) ([]int, error) {
	if field == "*" {
		return makeRange(min, max), nil
	}

	if strings.HasPrefix(field, "*/") {
		step, err := strconv.Atoi(field[2:])
		if err != nil || step < 1 {
			return nil, fmt.Errorf("invalid step value: %s", field)
		}
		return makeStep(min, max, step), nil
	}

	// Handle comma-separated values
	if strings.Contains(field, ",") {
		var result []int
		for _, part := range strings.Split(field, ",") {
			vals, err := parseField(part, min, max)
			if err != nil {
				return nil, err
			}
			result = append(result, vals...)
		}
		return dedupe(result), nil
	}

	// Handle range
	if strings.Contains(field, "-") {
		parts := strings.SplitN(field, "-", 2)
		start, err := strconv.Atoi(parts[0])
		if err != nil {
			return nil, fmt.Errorf("invalid range start: %s", parts[0])
		}
		end, err := strconv.Atoi(parts[1])
		if err != nil {
			return nil, fmt.Errorf("invalid range end: %s", parts[1])
		}
		if start < min || end > max || start > end {
			return nil, fmt.Errorf("invalid range %d-%d (valid: %d-%d)", start, end, min, max)
		}
		return makeRange(start, end), nil
	}

	// Single value
	val, err := strconv.Atoi(field)
	if err != nil {
		return nil, fmt.Errorf("invalid value: %s", field)
	}
	if val < min || val > max {
		return nil, fmt.Errorf("value %d out of range %d-%d", val, min, max)
	}
	return []int{val}, nil
}

// makeRange creates a slice of integers from start to end inclusive
func makeRange(start, end int) []int {
	result := make([]int, end-start+1)
	for i := range result {
		result[i] = start + i
	}
	return result
}

// makeStep creates a slice of integers from min to max with step interval
func makeStep(min, max, step int) []int {
	var result []int
	for i := min; i <= max; i += step {
		result = append(result, i)
	}
	return result
}

// dedupe removes duplicates and sorts
func dedupe(vals []int) []int {
	seen := make(map[int]bool)
	var result []int
	for _, v := range vals {
		if !seen[v] {
			seen[v] = true
			result = append(result, v)
		}
	}
	// Simple insertion sort (small slices)
	for i := 1; i < len(result); i++ {
		for j := i; j > 0 && result[j] < result[j-1]; j-- {
			result[j], result[j-1] = result[j-1], result[j]
		}
	}
	return result
}

// contains checks if a value is in a slice
func contains(vals []int, v int) bool {
	for _, val := range vals {
		if val == v {
			return true
		}
	}
	return false
}

// Next returns the next time the cron expression matches after the given time
func (c *CronExpr) Next(after time.Time) time.Time {
	// Start from the next minute
	t := after.Truncate(time.Minute).Add(time.Minute)

	// Search up to 5 years ahead (enough for any valid cron)
	maxIterations := 5 * 366 * 24 * 60
	for i := 0; i < maxIterations; i++ {
		if c.matches(t) {
			return t
		}
		t = t.Add(time.Minute)
	}

	// Should never happen with valid cron expressions
	return time.Time{}
}

// matches checks if the given time matches the cron expression
func (c *CronExpr) matches(t time.Time) bool {
	if !contains(c.Minutes, t.Minute()) {
		return false
	}
	if !contains(c.Hours, t.Hour()) {
		return false
	}
	if !contains(c.Months, int(t.Month())) {
		return false
	}

	// Day matching: match if day-of-month OR day-of-week matches
	// This follows standard cron behavior when both are specified as non-*
	dayOfMonthMatch := contains(c.DaysOfMonth, t.Day())
	dayOfWeekMatch := contains(c.DaysOfWeek, int(t.Weekday()))

	// If both fields are *, match any day
	if len(c.DaysOfMonth) == 31 && len(c.DaysOfWeek) == 7 {
		return true
	}

	// If only one is *, use the other
	if len(c.DaysOfMonth) == 31 {
		return dayOfWeekMatch
	}
	if len(c.DaysOfWeek) == 7 {
		return dayOfMonthMatch
	}

	// Both are restricted: match if either matches
	return dayOfMonthMatch || dayOfWeekMatch
}
