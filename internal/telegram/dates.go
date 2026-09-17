package telegram

import (
	"fmt"
	"strings"
	"time"

	"github.com/avarabyeu/tripops-bot/internal/core"
)

// parseDateRange reads the handful of date shapes a person actually types into
// a chat. Typing is the thing the product tries hardest to avoid, so the
// parser is forgiving rather than strict:
//
//	23.09              a single day
//	23.09 - 24.09      a range
//	23/09-24/09
//	2026-09-23         ISO, single day
//	2026-09-23 2026-09-24
//
// A day/month without a year means the next occurrence: typing "23.09" in
// December means next September, not one that has already passed.
func parseDateRange(input string, now time.Time) (core.Date, core.Date, error) {
	cleaned := strings.NewReplacer("—", "-", "–", "-", " to ", "-", "…", "-").Replace(input)
	fields := splitRange(cleaned)
	if len(fields) == 0 {
		return core.Date{}, core.Date{}, fmt.Errorf("telegram: no dates in %q", input)
	}

	start, err := parseOneDate(fields[0], now)
	if err != nil {
		return core.Date{}, core.Date{}, err
	}
	if len(fields) == 1 {
		return start, start, nil
	}
	end, err := parseOneDate(fields[1], now)
	if err != nil {
		return core.Date{}, core.Date{}, err
	}
	if end.Before(start) {
		// "30.12 - 02.01" is a new year's trip, not a mistake.
		end = core.NewDate(end.Year+1, end.Month, end.Day)
	}
	return start, end, nil
}

// splitRange divides the input on a dash or whitespace, keeping at most two parts.
func splitRange(s string) []string {
	var parts []string
	for _, chunk := range strings.FieldsFunc(s, func(r rune) bool {
		return r == '-' || r == ' ' || r == '\t' || r == ','
	}) {
		chunk = strings.TrimSpace(chunk)
		if chunk == "" {
			continue
		}
		parts = append(parts, chunk)
	}
	// An ISO date contains dashes, so rebuild it when the split shredded one.
	if len(parts) >= 3 && len(parts[0]) == 4 {
		joined := []string{strings.Join(parts[0:3], "-")}
		if len(parts) >= 6 && len(parts[3]) == 4 {
			joined = append(joined, strings.Join(parts[3:6], "-"))
		}
		return joined
	}
	if len(parts) > 2 {
		parts = parts[:2]
	}
	return parts
}

func parseOneDate(s string, now time.Time) (core.Date, error) {
	s = strings.TrimSpace(strings.ReplaceAll(s, "/", "."))

	for _, layout := range []string{"2006-01-02", "02.01.2006", "2.1.2006"} {
		if t, err := time.Parse(layout, s); err == nil {
			return core.DateOf(t, time.UTC), nil
		}
	}
	for _, layout := range []string{"02.01", "2.1"} {
		if t, err := time.Parse(layout, s); err == nil {
			candidate := core.NewDate(now.Year(), t.Month(), t.Day())
			if candidate.Before(core.DateOf(now, time.UTC)) {
				candidate = core.NewDate(now.Year()+1, t.Month(), t.Day())
			}
			return candidate, nil
		}
	}
	return core.Date{}, fmt.Errorf("telegram: %q is not a date", s)
}
