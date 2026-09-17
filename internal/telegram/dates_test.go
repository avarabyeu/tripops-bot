package telegram

import (
	"testing"
	"time"

	"github.com/avarabyeu/tripops-bot/internal/core"
)

// Creating a trip is the only place the bot asks for free text, so the date
// parser has to accept what a person actually types into a chat.
func TestParseDateRange(t *testing.T) {
	// A fixed "now" in mid-September, so the next-occurrence rule is testable.
	now := time.Date(2026, time.September, 16, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		in    string
		start core.Date
		end   core.Date
	}{
		{"23.09", core.NewDate(2026, time.September, 23), core.NewDate(2026, time.September, 23)},
		{"23.09 - 24.09", core.NewDate(2026, time.September, 23), core.NewDate(2026, time.September, 24)},
		{"23.09-24.09", core.NewDate(2026, time.September, 23), core.NewDate(2026, time.September, 24)},
		{"23/09 - 24/09", core.NewDate(2026, time.September, 23), core.NewDate(2026, time.September, 24)},
		{"2026-09-23", core.NewDate(2026, time.September, 23), core.NewDate(2026, time.September, 23)},
		{"2026-09-23 2026-09-24", core.NewDate(2026, time.September, 23), core.NewDate(2026, time.September, 24)},
		{"2026-09-23 - 2026-09-24", core.NewDate(2026, time.September, 23), core.NewDate(2026, time.September, 24)},
		{"23.09.2026", core.NewDate(2026, time.September, 23), core.NewDate(2026, time.September, 23)},
		{"3.10", core.NewDate(2026, time.October, 3), core.NewDate(2026, time.October, 3)},
		// An em dash is what a phone keyboard produces.
		{"23.09 — 24.09", core.NewDate(2026, time.September, 23), core.NewDate(2026, time.September, 24)},
		{"  23.09  ", core.NewDate(2026, time.September, 23), core.NewDate(2026, time.September, 23)},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			start, end, err := parseDateRange(tc.in, now)
			if err != nil {
				t.Fatalf("parseDateRange(%q) failed: %v", tc.in, err)
			}
			if start != tc.start {
				t.Errorf("start = %v, want %v", start, tc.start)
			}
			if end != tc.end {
				t.Errorf("end = %v, want %v", end, tc.end)
			}
		})
	}
}

// A day and month with no year means the next one, not one that has passed.
func TestParseDateRangeRollsForwardToNextYear(t *testing.T) {
	december := time.Date(2026, time.December, 20, 12, 0, 0, 0, time.UTC)

	start, end, err := parseDateRange("23.09", december)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := core.NewDate(2027, time.September, 23); start != want {
		t.Errorf("start = %v, want %v — a past date should mean next year", start, want)
	}
	if end != start {
		t.Errorf("end = %v, want the same day", end)
	}
}

// "30.12 - 02.01" is a new year's trip, not a mistake.
func TestParseDateRangeSpansNewYear(t *testing.T) {
	now := time.Date(2026, time.December, 1, 12, 0, 0, 0, time.UTC)

	start, end, err := parseDateRange("30.12 - 02.01", now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := core.NewDate(2026, time.December, 30); start != want {
		t.Errorf("start = %v, want %v", start, want)
	}
	if want := core.NewDate(2027, time.January, 2); end != want {
		t.Errorf("end = %v, want %v — the range must not run backwards", end, want)
	}
	if end.Before(start) {
		t.Error("end is before start")
	}
}

func TestParseDateRangeRejectsNonsense(t *testing.T) {
	now := time.Date(2026, time.September, 16, 12, 0, 0, 0, time.UTC)

	for _, in := range []string{"", "   ", "tomorrow", "next week", "99.99", "13"} {
		t.Run(in, func(t *testing.T) {
			if _, _, err := parseDateRange(in, now); err == nil {
				t.Errorf("parseDateRange(%q) should have failed", in)
			}
		})
	}
}

// Every piece of user text in a message goes through this; a stray angle
// bracket would otherwise break Telegram's HTML parse mode or inject markup.
func TestEscapeHTML(t *testing.T) {
	cases := map[string]string{
		"plain":                 "plain",
		"Bob & Alice":           "Bob &amp; Alice",
		"<b>bold</b>":           "&lt;b&gt;bold&lt;/b&gt;",
		`<a href="x">click</a>`: `&lt;a href="x"&gt;click&lt;/a&gt;`,
		"5 < 6 > 4 & true":      "5 &lt; 6 &gt; 4 &amp; true",
	}
	for in, want := range cases {
		if got := EscapeHTML(in); got != want {
			t.Errorf("EscapeHTML(%q) = %q, want %q", in, got, want)
		}
	}
}

// Callback payloads must fit Telegram's 64 byte limit, which is why ids travel
// in their compact form.
func TestCallbackPayloadsFitTelegramsLimit(t *testing.T) {
	decision, option := core.NewID(), core.NewID()

	payload := "dv:" + decision.Compact() + ":" + option.Compact()
	if len(payload) > 64 {
		t.Errorf("a vote payload is %d bytes, over Telegram's 64 byte limit", len(payload))
	}
	// The same payload with dashed uuids is what this encoding avoids.
	if len("dv:"+decision.String()+":"+option.String()) <= 64 {
		t.Error("dashed uuids now fit, so the compact form may no longer be needed")
	}

	settlement := "sm:" + core.NewID().Compact() + ":" + core.NewID().Compact() + ":" + itoa(1234567)
	if len(settlement) > 64 {
		t.Errorf("a settlement payload is %d bytes, over the limit", len(settlement))
	}
}

func TestTruncate(t *testing.T) {
	cases := []struct {
		in   string
		max  int
		want string
	}{
		{"short", 10, "short"},
		{"exactly-10", 10, "exactly-10"},
		{"this is far too long", 10, "this is f…"},
		// Runes, not bytes: a multi-byte name must not be cut in half, and the
		// ellipsis counts towards the budget.
		{"Łódź Brevet", 6, "Łódź …"},
		{"Łódź Brevet", 5, "Łódź…"},
	}
	for _, tc := range cases {
		if got := truncate(tc.in, tc.max); got != tc.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", tc.in, tc.max, got, tc.want)
		}
	}
}
