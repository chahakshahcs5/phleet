package cron

import (
	"testing"
	"time"
)

func at(t *testing.T, value string) time.Time {
	t.Helper()
	loc := time.FixedZone("test", 0)
	parsed, err := time.ParseInLocation("2006-01-02 15:04", value, loc)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func TestParseAndMatches(t *testing.T) {
	s, err := Parse("*/15 * * * *")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		at   string
		want bool
	}{
		{"2026-01-01 10:00", true},
		{"2026-01-01 10:15", true},
		{"2026-01-01 10:07", false},
	} {
		if got := s.Matches(at(t, tc.at)); got != tc.want {
			t.Fatalf("Matches(%s) = %v, want %v", tc.at, got, tc.want)
		}
	}
}

func TestNamesAndRanges(t *testing.T) {
	s, err := Parse("0 9 * * mon-fri")
	if err != nil {
		t.Fatal(err)
	}
	if !s.Matches(at(t, "2026-11-03 09:00")) { // Tuesday
		t.Fatal("Tuesday 09:00 should match")
	}
	if s.Matches(at(t, "2026-11-07 09:00")) { // Saturday
		t.Fatal("Saturday 09:00 should not match")
	}
	s, err = Parse("0 0 1 jan,jul *")
	if err != nil {
		t.Fatal(err)
	}
	if !s.Matches(at(t, "2026-07-01 00:00")) || s.Matches(at(t, "2026-07-02 00:00")) {
		t.Fatal("month list did not match")
	}
}

func TestDayOfWeekSevenIsSunday(t *testing.T) {
	for _, expr := range []string{"0 0 * * 0", "0 0 * * 7"} {
		s, err := Parse(expr)
		if err != nil {
			t.Fatal(err)
		}
		if !s.Matches(at(t, "2026-11-08 00:00")) { // Sunday
			t.Fatalf("%s did not match Sunday", expr)
		}
	}
}

func TestDomAndDowUseOrSemantics(t *testing.T) {
	// Every Friday and every 13th of the month.
	s, err := Parse("0 0 13 * 5")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		at   string
		want bool
	}{
		{"2026-11-13 00:00", true}, // Friday the 13th
		{"2026-11-06 00:00", true}, // Friday
		{"2026-11-20 00:00", true}, // Friday
		{"2026-11-03 00:00", false},
	} {
		if got := s.Matches(at(t, tc.at)); got != tc.want {
			t.Fatalf("Matches(%s) = %v, want %v", tc.at, got, tc.want)
		}
	}
}

func TestNext(t *testing.T) {
	cases := []struct {
		expr string
		from string
		want string
	}{
		{"*/15 * * * *", "2026-01-01 10:07", "2026-01-01 10:15"},
		{"0 3 * * *", "2026-01-01 04:00", "2026-01-02 03:00"},
		{"0 0 1 * *", "2026-01-15 12:00", "2026-02-01 00:00"},
		{"0 0 * * 0", "2026-11-03 12:00", "2026-11-08 00:00"},
		{"@daily", "2026-11-03 12:00", "2026-11-04 00:00"},
	}
	for _, tc := range cases {
		s, err := Parse(tc.expr)
		if err != nil {
			t.Fatalf("%s: %v", tc.expr, err)
		}
		got := s.Next(at(t, tc.from))
		if want := at(t, tc.want); !got.Equal(want) {
			t.Fatalf("Next(%s, %s) = %s, want %s", tc.expr, tc.from, got, want)
		}
	}
}

func TestImpossibleScheduleReturnsZero(t *testing.T) {
	s, err := Parse("0 0 31 2 *")
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Next(at(t, "2026-01-01 00:00")); !got.IsZero() {
		t.Fatalf("expected zero time, got %s", got)
	}
}

func TestParseErrors(t *testing.T) {
	for _, expr := range []string{
		"",
		"* * * *",
		"60 * * * *",
		"* 24 * * *",
		"* * 0 * *",
		"* * * 13 *",
		"* * * * 8",
		"*/0 * * * *",
		"5-1 * * * *",
		"@sometimes",
	} {
		if _, err := Parse(expr); err == nil {
			t.Fatalf("Parse(%q) should fail", expr)
		}
	}
}
