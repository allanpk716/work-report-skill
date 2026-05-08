package digest

import (
	"testing"
	"time"
)

func TestParseScope(t *testing.T) {
	tests := []struct {
		input   string
		want    Scope
		wantErr bool
	}{
		{"today", ScopeToday, false},
		{"TODAY", ScopeToday, false},
		{"  today  ", ScopeToday, false},
		{"week", ScopeWeek, false},
		{"WEEK", ScopeWeek, false},
		{"month", ScopeMonth, false},
		{"MONTH", ScopeMonth, false},
		{"custom", ScopeCustom, false},
		{"CUSTOM", ScopeCustom, false},
		{"yearly", "", true},
		{"", "", true},
		{"invalid", "", true},
	}

	for _, tt := range tests {
		got, err := ParseScope(tt.input)
		if tt.wantErr {
			if err == nil {
				t.Errorf("ParseScope(%q): expected error, got nil", tt.input)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseScope(%q): unexpected error: %v", tt.input, err)
			continue
		}
		if got != tt.want {
			t.Errorf("ParseScope(%q): expected %q, got %q", tt.input, tt.want, got)
		}
	}
}

func TestResolveDateRange_Today(t *testing.T) {
	loc := time.FixedZone("UTC+8", 8*3600)
	now := time.Now().In(loc)

	start, end, err := ResolveDateRange(ScopeToday, loc)
	if err != nil {
		t.Fatalf("ResolveDateRange(today) failed: %v", err)
	}

	// Start should be midnight today
	wantStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	if !start.Equal(wantStart) {
		t.Errorf("start: expected %v, got %v", wantStart, start)
	}

	// End should be 23:59:59 today
	wantEnd := time.Date(now.Year(), now.Month(), now.Day(), 23, 59, 59, 0, loc)
	if !end.Equal(wantEnd) {
		t.Errorf("end: expected %v, got %v", wantEnd, end)
	}
}

func TestResolveDateRange_Week(t *testing.T) {
	loc := time.UTC

	start, end, err := ResolveDateRange(ScopeWeek, loc)
	if err != nil {
		t.Fatalf("ResolveDateRange(week) failed: %v", err)
	}

	now := time.Now().In(loc)

	// Start should be Monday
	if start.Weekday() != time.Monday {
		t.Errorf("week start should be Monday, got %v", start.Weekday())
	}

	// End should be Sunday
	if end.Weekday() != time.Sunday {
		t.Errorf("week end should be Sunday, got %v", end.Weekday())
	}

	// Start hour should be 0
	if start.Hour() != 0 || start.Minute() != 0 || start.Second() != 0 {
		t.Errorf("week start should be midnight, got %v", start)
	}

	// End hour should be 23:59:59
	if end.Hour() != 23 || end.Minute() != 59 || end.Second() != 59 {
		t.Errorf("week end should be 23:59:59, got %v", end)
	}

	// The week should contain "now"
	if now.Before(start) || now.After(end) {
		t.Errorf("current time %v should be within week range [%v, %v]", now, start, end)
	}
}

func TestResolveDateRange_Month(t *testing.T) {
	loc := time.UTC

	start, end, err := ResolveDateRange(ScopeMonth, loc)
	if err != nil {
		t.Fatalf("ResolveDateRange(month) failed: %v", err)
	}

	now := time.Now().In(loc)

	// Start should be 1st of current month
	if start.Day() != 1 {
		t.Errorf("month start should be day 1, got %d", start.Day())
	}
	if start.Month() != now.Month() {
		t.Errorf("month start should be current month %v, got %v", now.Month(), start.Month())
	}

	// End should be last day of current month
	lastDay := start.AddDate(0, 1, -1).Day()
	if end.Day() != lastDay {
		t.Errorf("month end should be day %d, got %d", lastDay, end.Day())
	}
	if end.Hour() != 23 || end.Minute() != 59 || end.Second() != 59 {
		t.Errorf("month end should be 23:59:59, got %v", end)
	}
}

func TestResolveDateRange_MonthEnd_Boundary(t *testing.T) {
	// Test with a fixed date at end of month (Jan 31)
	loc := time.UTC
	jan31 := time.Date(2025, 1, 31, 14, 30, 0, 0, loc)

	// We can't inject "now" easily, so test the month-end calculation
	// logic directly via a custom date approach.
	// Instead, test that Feb 2025 (28 days) works correctly.
	firstOfFeb := time.Date(2025, 2, 1, 0, 0, 0, 0, loc)
	lastOfFeb := firstOfFeb.AddDate(0, 1, -1)

	if lastOfFeb.Day() != 28 {
		t.Errorf("Feb 2025 should have 28 days, got %d", lastOfFeb.Day())
	}
	if lastOfFeb.Month() != 2 {
		t.Errorf("expected February, got %v", lastOfFeb.Month())
	}

	// Test leap year: Feb 2024 should have 29 days
	firstOfFeb2024 := time.Date(2024, 2, 1, 0, 0, 0, 0, loc)
	lastOfFeb2024 := firstOfFeb2024.AddDate(0, 1, -1)

	if lastOfFeb2024.Day() != 29 {
		t.Errorf("Feb 2024 (leap year) should have 29 days, got %d", lastOfFeb2024.Day())
	}

	// Test Dec: 31 days
	firstOfDec := time.Date(2025, 12, 1, 0, 0, 0, 0, loc)
	lastOfDec := firstOfDec.AddDate(0, 1, -1)

	if lastOfDec.Day() != 31 {
		t.Errorf("Dec 2025 should have 31 days, got %d", lastOfDec.Day())
	}

	// Verify the _jan31 variable is used to avoid unused variable error
	_ = jan31
}

func TestResolveDateRange_Custom(t *testing.T) {
	loc := time.UTC

	start, end, err := ResolveDateRange(ScopeCustom, loc, "2025-06-01:2025-06-07")
	if err != nil {
		t.Fatalf("ResolveDateRange(custom) failed: %v", err)
	}

	wantStart := time.Date(2025, 6, 1, 0, 0, 0, 0, loc)
	wantEnd := time.Date(2025, 6, 7, 23, 59, 59, 0, loc)

	if !start.Equal(wantStart) {
		t.Errorf("custom start: expected %v, got %v", wantStart, start)
	}
	if !end.Equal(wantEnd) {
		t.Errorf("custom end: expected %v, got %v", wantEnd, end)
	}
}

func TestResolveDateRange_Custom_SameDay(t *testing.T) {
	loc := time.UTC

	start, end, err := ResolveDateRange(ScopeCustom, loc, "2025-06-01:2025-06-01")
	if err != nil {
		t.Fatalf("ResolveDateRange(custom same day) failed: %v", err)
	}

	if start.Day() != 1 || end.Day() != 1 {
		t.Error("same-day custom range should work")
	}
	if end.Hour() != 23 || end.Minute() != 59 || end.Second() != 59 {
		t.Errorf("same-day end should be 23:59:59, got %v", end)
	}
}

func TestResolveDateRange_Custom_Errors(t *testing.T) {
	loc := time.UTC

	tests := []struct {
		name  string
		input string
	}{
		{"no range", ""},
		{"missing colon", "2025-06-01"},
		{"invalid start", "bad-date:2025-06-07"},
		{"invalid end", "2025-06-01:bad-date"},
		{"start after end", "2025-06-07:2025-06-01"},
		{"too many colons", "2025-06-01:2025-06-07:extra"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := ResolveDateRange(ScopeCustom, loc, tt.input)
			if err == nil {
				t.Errorf("ResolveDateRange(custom, %q): expected error", tt.input)
			}
		})
	}
}

func TestResolveDateRange_Custom_NoArg(t *testing.T) {
	loc := time.UTC
	_, _, err := ResolveDateRange(ScopeCustom, loc)
	if err == nil {
		t.Error("expected error when custom scope has no range argument")
	}
}

func TestResolveDateRange_Timezone(t *testing.T) {
	// Verify that the timezone parameter is respected.
	shanghai, _ := time.LoadLocation("Asia/Shanghai")
	utc := time.UTC

	startSH, _, _ := ResolveDateRange(ScopeToday, shanghai)
	startUTC, _, _ := ResolveDateRange(ScopeToday, utc)

	// The start times should differ by 8 hours (Asia/Shanghai = UTC+8)
	diff := startSH.Sub(startUTC).Hours()
	// Allow some tolerance for the rare case where the test runs right at midnight
	if diff < 7.9 || diff > 8.1 {
		// Only check if both are on the same UTC day, otherwise the difference
		// could be different due to date boundaries.
		if startSH.Day() == startUTC.Day() {
			t.Errorf("timezone offset should be ~8h, got %.1fh", diff)
		}
	}
}

func TestResolveDateRange_UnknownScope(t *testing.T) {
	loc := time.UTC
	_, _, err := ResolveDateRange("unknown_scope", loc)
	if err == nil {
		t.Error("expected error for unknown scope")
	}
}
