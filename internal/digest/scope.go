package digest

import (
	"fmt"
	"strings"
	"time"
)

// Scope defines the time range for a digest query.
type Scope string

const (
	// ScopeToday covers the current calendar day.
	ScopeToday Scope = "today"
	// ScopeWeek covers the current ISO week (Monday–Sunday).
	ScopeWeek Scope = "week"
	// ScopeMonth covers the current calendar month.
	ScopeMonth Scope = "month"
	// ScopeCustom covers a user-defined date range in YYYY-MM-DD:YYYY-MM-DD format.
	ScopeCustom Scope = "custom"
)

// ValidScopes lists all accepted scope values.
var ValidScopes = []Scope{ScopeToday, ScopeWeek, ScopeMonth, ScopeCustom}

// ParseScope converts a string to a Scope, returning an error if the value
// is not one of the recognized scopes.
func ParseScope(s string) (Scope, error) {
	scope := Scope(strings.ToLower(strings.TrimSpace(s)))
	for _, valid := range ValidScopes {
		if scope == valid {
			return scope, nil
		}
	}
	return "", fmt.Errorf("digest: invalid scope %q (valid: %s)", s, strings.Join(scopeStrings(), ", "))
}

// scopeStrings returns ValidScopes as string slice for error messages.
func scopeStrings() []string {
	out := make([]string, len(ValidScopes))
	for i, s := range ValidScopes {
		out[i] = string(s)
	}
	return out
}

// ResolveDateRange returns the start and end time for a given scope,
// anchored to the provided location. For ScopeCustom, the scope string
// must be in the format "custom:YYYY-MM-DD:YYYY-MM-DD".
//
// The returned range is inclusive on both ends: start is 00:00:00 of the
// start date, end is 23:59:59 of the end date.
func ResolveDateRange(scope Scope, loc *time.Location, customRange ...string) (start, end time.Time, err error) {
	now := time.Now().In(loc)

	switch scope {
	case ScopeToday:
		start = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
		end = time.Date(now.Year(), now.Month(), now.Day(), 23, 59, 59, 0, loc)

	case ScopeWeek:
		// ISO week: Monday is the first day.
		weekday := now.Weekday()
		if weekday == time.Sunday {
			weekday = 7 // ISO: Sunday = 7
		}
		monday := now.AddDate(0, 0, -int(weekday-time.Monday))
		start = time.Date(monday.Year(), monday.Month(), monday.Day(), 0, 0, 0, 0, loc)
		sunday := monday.AddDate(0, 0, 6)
		end = time.Date(sunday.Year(), sunday.Month(), sunday.Day(), 23, 59, 59, 0, loc)

	case ScopeMonth:
		start = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc)
		// Last day of the month: go to the 1st of next month, subtract 1 day.
		firstOfNext := start.AddDate(0, 1, 0)
		lastOfMonth := firstOfNext.AddDate(0, 0, -1)
		end = time.Date(lastOfMonth.Year(), lastOfMonth.Month(), lastOfMonth.Day(), 23, 59, 59, 0, loc)

	case ScopeCustom:
		if len(customRange) == 0 || customRange[0] == "" {
			return time.Time{}, time.Time{}, fmt.Errorf("digest: custom scope requires a date range (YYYY-MM-DD:YYYY-MM-DD)")
		}
		start, end, err = parseCustomRange(customRange[0], loc)
		if err != nil {
			return time.Time{}, time.Time{}, err
		}

	default:
		return time.Time{}, time.Time{}, fmt.Errorf("digest: unknown scope %q", scope)
	}

	return start, end, nil
}

// parseCustomRange parses "YYYY-MM-DD:YYYY-MM-DD" into inclusive start/end times.
func parseCustomRange(s string, loc *time.Location) (time.Time, time.Time, error) {
	parts := strings.SplitN(s, ":", 2)
	if len(parts) != 2 {
		return time.Time{}, time.Time{}, fmt.Errorf("digest: custom range must be YYYY-MM-DD:YYYY-MM-DD, got %q", s)
	}

	startDate, err := time.Parse("2006-01-02", strings.TrimSpace(parts[0]))
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("digest: invalid start date %q: %w", parts[0], err)
	}

	endDate, err := time.Parse("2006-01-02", strings.TrimSpace(parts[1]))
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("digest: invalid end date %q: %w", parts[1], err)
	}

	if startDate.After(endDate) {
		return time.Time{}, time.Time{}, fmt.Errorf("digest: start date %q must be before or equal to end date %q", parts[0], parts[1])
	}

	start := time.Date(startDate.Year(), startDate.Month(), startDate.Day(), 0, 0, 0, 0, loc)
	end := time.Date(endDate.Year(), endDate.Month(), endDate.Day(), 23, 59, 59, 0, loc)

	return start, end, nil
}
