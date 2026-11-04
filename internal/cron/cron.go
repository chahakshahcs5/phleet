// Package cron implements the subset of 5-field cron expressions phleet uses
// for scheduled tasks. It is shared by the control plane (next-run display) and
// the agent (local execution), so a schedule means the same thing on both.
package cron

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Schedule is a parsed cron expression. It is safe for concurrent use.
type Schedule struct {
	expr          string
	minute        field
	hour          field
	dom           field
	month         field
	dow           field
	domRestricted bool
	dowRestricted bool
}

type field struct {
	bits uint64
}

func (f field) match(v int) bool {
	return v >= 0 && v < 64 && f.bits&(1<<uint(v)) != 0
}

var monthNames = map[string]int{
	"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6,
	"jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12,
}

var dayNames = map[string]int{
	"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6,
}

// Parse accepts 5 fields (minute hour day-of-month month day-of-week) and the
// common @hourly/@daily/@weekly/@monthly/@yearly aliases.
func Parse(expr string) (Schedule, error) {
	e := strings.TrimSpace(expr)
	if e == "" {
		return Schedule{}, fmt.Errorf("cron expression is empty")
	}
	if strings.HasPrefix(e, "@") {
		switch strings.ToLower(e) {
		case "@hourly":
			e = "0 * * * *"
		case "@daily", "@midnight":
			e = "0 0 * * *"
		case "@weekly":
			e = "0 0 * * 0"
		case "@monthly":
			e = "0 0 1 * *"
		case "@yearly", "@annually":
			e = "0 0 1 1 *"
		default:
			return Schedule{}, fmt.Errorf("unsupported cron alias %q", e)
		}
	}
	parts := strings.Fields(e)
	if len(parts) != 5 {
		return Schedule{}, fmt.Errorf("cron needs 5 fields (minute hour day month weekday)")
	}
	minute, _, err := parseField(parts[0], 0, 59, nil)
	if err != nil {
		return Schedule{}, fmt.Errorf("minute: %w", err)
	}
	hour, _, err := parseField(parts[1], 0, 23, nil)
	if err != nil {
		return Schedule{}, fmt.Errorf("hour: %w", err)
	}
	dom, domRestricted, err := parseField(parts[2], 1, 31, nil)
	if err != nil {
		return Schedule{}, fmt.Errorf("day of month: %w", err)
	}
	month, _, err := parseField(parts[3], 1, 12, monthNames)
	if err != nil {
		return Schedule{}, fmt.Errorf("month: %w", err)
	}
	dow, dowRestricted, err := parseField(parts[4], 0, 7, dayNames)
	if err != nil {
		return Schedule{}, fmt.Errorf("day of week: %w", err)
	}
	// Both 0 and 7 mean Sunday. fold 7 into 0 so match uses time.Weekday.
	if dow.bits&(1<<7) != 0 {
		dow.bits |= 1
		dow.bits &^= 1 << 7
	}
	return Schedule{
		expr: e, minute: minute, hour: hour, dom: dom, month: month, dow: dow,
		domRestricted: domRestricted, dowRestricted: dowRestricted,
	}, nil
}

func parseField(raw string, min, max int, names map[string]int) (field, bool, error) {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" {
		return field{}, false, fmt.Errorf("empty field")
	}
	restricted := raw != "*" && raw != "?"
	var bits uint64
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return field{}, false, fmt.Errorf("empty list item")
		}
		base := part
		step := 1
		if i := strings.IndexByte(part, '/'); i >= 0 {
			base = strings.TrimSpace(part[:i])
			n, err := strconv.Atoi(strings.TrimSpace(part[i+1:]))
			if err != nil || n <= 0 {
				return field{}, false, fmt.Errorf("invalid step %q", part)
			}
			step = n
		}
		lo, hi, err := parseRange(base, min, max, names, step)
		if err != nil {
			return field{}, false, err
		}
		for v := lo; v <= hi; v += step {
			bits |= 1 << uint(v)
		}
	}
	if bits == 0 {
		return field{}, false, fmt.Errorf("field matches nothing")
	}
	return field{bits: bits}, restricted, nil
}

func parseRange(base string, min, max int, names map[string]int, step int) (int, int, error) {
	switch {
	case base == "*" || base == "?":
		return min, max, nil
	case strings.Contains(base, "-"):
		ab := strings.SplitN(base, "-", 2)
		lo, err := parseValue(ab[0], names)
		if err != nil {
			return 0, 0, err
		}
		hi, err := parseValue(ab[1], names)
		if err != nil {
			return 0, 0, err
		}
		if lo > hi {
			return 0, 0, fmt.Errorf("range %q is reversed", base)
		}
		if lo < min || hi > max {
			return 0, 0, fmt.Errorf("range %q is outside %d-%d", base, min, max)
		}
		return lo, hi, nil
	default:
		v, err := parseValue(base, names)
		if err != nil {
			return 0, 0, err
		}
		if v < min || v > max {
			return 0, 0, fmt.Errorf("value %d is outside %d-%d", v, min, max)
		}
		hi := v
		if step > 1 {
			// "5/10" means 5,15,25,... through the end of the range.
			hi = max
		}
		return v, hi, nil
	}
}

func parseValue(v string, names map[string]int) (int, error) {
	v = strings.ToLower(strings.TrimSpace(v))
	if names != nil {
		if n, ok := names[v]; ok {
			return n, nil
		}
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("invalid value %q", v)
	}
	return n, nil
}

func (s Schedule) dayMatches(t time.Time) bool {
	dom := s.dom.match(t.Day())
	dow := s.dow.match(int(t.Weekday()))
	// Standard cron: when both day fields are restricted, either may match.
	if s.domRestricted && s.dowRestricted {
		return dom || dow
	}
	if s.domRestricted {
		return dom
	}
	if s.dowRestricted {
		return dow
	}
	return true
}

// Matches reports whether the schedule fires in the minute containing t.
func (s Schedule) Matches(t time.Time) bool {
	return s.minute.match(t.Minute()) &&
		s.hour.match(t.Hour()) &&
		s.month.match(int(t.Month())) &&
		s.dayMatches(t)
}

// Next returns the first matching minute strictly after from. It returns the
// zero time if the expression cannot fire within five years (for example 31
// February), so callers can display "never" instead of looping forever.
func (s Schedule) Next(from time.Time) time.Time {
	t := from.Truncate(time.Minute).Add(time.Minute)
	limit := from.AddDate(5, 0, 0)
	for t.Before(limit) {
		if !s.month.match(int(t.Month())) {
			t = time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, t.Location())
			continue
		}
		if !s.dayMatches(t) {
			t = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, t.Location())
			continue
		}
		if !s.hour.match(t.Hour()) {
			t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour()+1, 0, 0, 0, t.Location())
			continue
		}
		if !s.minute.match(t.Minute()) {
			t = t.Add(time.Minute)
			continue
		}
		return t
	}
	return time.Time{}
}

func (s Schedule) String() string { return s.expr }
