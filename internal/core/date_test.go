package core

import (
	"errors"
	"testing"
	"time"
)

func TestParseAndFormatDate(t *testing.T) {
	d, err := ParseDate("2026-09-23")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Year != 2026 || d.Month != time.September || d.Day != 23 {
		t.Fatalf("parsed %+v", d)
	}
	if d.String() != "2026-09-23" {
		t.Errorf("String() = %q", d.String())
	}
	if _, err := ParseDate("23/09/2026"); err == nil {
		t.Error("expected an error for a non-ISO date")
	}
}

func TestDateArithmetic(t *testing.T) {
	start := NewDate(2026, time.September, 23)
	end := NewDate(2026, time.September, 24)

	if got := start.DaysUntil(end); got != 1 {
		t.Errorf("DaysUntil = %d, want 1", got)
	}
	if got := start.Nights(end); got != 1 {
		t.Errorf("Nights = %d, want 1", got)
	}
	if got := start.Nights(start); got != 0 {
		t.Errorf("a same-day trip has %d nights, want 0", got)
	}
	if !start.Before(end) || !end.After(start) {
		t.Error("ordering is wrong")
	}
	if got := start.AddDays(8); got != NewDate(2026, time.October, 1) {
		t.Errorf("AddDays crossed the month wrong: %v", got)
	}
}

// A date is the same calendar day everywhere; the instant it maps to is not.
func TestDateInLocation(t *testing.T) {
	warsaw, err := time.LoadLocation("Europe/Warsaw")
	if err != nil {
		t.Skipf("timezone database unavailable: %v", err)
	}
	d := NewDate(2026, time.September, 23)
	midnight := d.In(warsaw)
	if midnight.Hour() != 0 || midnight.Day() != 23 {
		t.Errorf("midnight in Warsaw = %v", midnight)
	}
	if utc := midnight.UTC(); utc.Day() != 22 || utc.Hour() != 22 {
		t.Errorf("Warsaw midnight in UTC = %v, want 2026-09-22T22:00Z", utc)
	}
	if got := d.EndOfDayIn(warsaw); !got.Equal(NewDate(2026, time.September, 24).In(warsaw)) {
		t.Errorf("EndOfDayIn = %v", got)
	}
}

func TestLoadLocationFallsBackToUTC(t *testing.T) {
	if got := LoadLocation("Mars/Olympus"); got != time.UTC {
		t.Errorf("unknown zone should fall back to UTC, got %v", got)
	}
	if IsValidTimezone("Mars/Olympus") {
		t.Error("unknown zone must not validate")
	}
	if !IsValidTimezone("UTC") {
		t.Error("UTC must validate")
	}
}

func TestIDRoundTrip(t *testing.T) {
	id := NewID()
	parsed, err := ParseID(id.String())
	if err != nil || parsed != id {
		t.Fatalf("dashed round trip failed: %v %v", parsed, err)
	}
	compact := id.Compact()
	if len(compact) != 22 {
		t.Fatalf("compact form is %d characters, want 22", len(compact))
	}
	back, err := ParseCompactID(compact)
	if err != nil || back != id {
		t.Fatalf("compact round trip failed: %v %v", back, err)
	}
	// ParseCompactID also accepts the canonical form so callers need not care.
	if back, err := ParseCompactID(id.String()); err != nil || back != id {
		t.Fatalf("compact parser should accept dashed ids: %v %v", back, err)
	}
	if _, err := ParseID("not-an-id"); err == nil {
		t.Error("expected an error for malformed input")
	}
}

func TestValidatorCollectsEveryProblem(t *testing.T) {
	v := NewValidator()
	v.Required("", "title")
	v.Length("x", "title", 3, 10) // first message for a field wins
	v.OneOf("blue", "colour", "red", "green")
	if v.OK() {
		t.Fatal("validator should have failed")
	}
	err := v.Err()
	var domain *Error
	if !errors.As(err, &domain) {
		t.Fatalf("expected a *core.Error, got %T", err)
	}
	if domain.Code != CodeInvalid {
		t.Errorf("code = %s", domain.Code)
	}
	if domain.Fields["title"] != "is required" {
		t.Errorf("title message = %q, want the first one recorded", domain.Fields["title"])
	}
	if _, ok := domain.Fields["colour"]; !ok {
		t.Error("colour should have failed OneOf")
	}
	if NewValidator().Err() != nil {
		t.Error("an empty validator must not produce an error")
	}
}
