package core

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

// Validator accumulates field level problems so a request can be rejected with
// everything that is wrong at once instead of one problem per round trip.
//
// It lives in core rather than in the HTTP layer because services validate
// their own input: the Telegram adapter and the REST API must not be able to
// bypass a rule by constructing a command differently.
type Validator struct {
	fields map[string]string
	order  []string
}

func NewValidator() *Validator { return &Validator{fields: map[string]string{}} }

// Check records msg against field when ok is false. The first message for a
// field wins, so rules can be written most-specific-first.
func (v *Validator) Check(ok bool, field, msg string) {
	if ok {
		return
	}
	if _, exists := v.fields[field]; exists {
		return
	}
	v.fields[field] = msg
	v.order = append(v.order, field)
}

// Required trims the value and fails when it is empty.
func (v *Validator) Required(value, field string) {
	v.Check(strings.TrimSpace(value) != "", field, "is required")
}

// Length fails when the trimmed value is outside [min, max] runes.
func (v *Validator) Length(value, field string, min, max int) {
	n := utf8.RuneCountInString(strings.TrimSpace(value))
	switch {
	case n < min:
		v.Check(false, field, fmt.Sprintf("must be at least %d characters", min))
	case n > max:
		v.Check(false, field, fmt.Sprintf("must be at most %d characters", max))
	}
}

// OneOf fails when value is not part of allowed.
func (v *Validator) OneOf(value, field string, allowed ...string) {
	for _, a := range allowed {
		if value == a {
			return
		}
	}
	v.Check(false, field, "must be one of: "+strings.Join(allowed, ", "))
}

// Err returns nil when nothing failed, otherwise an invalid *Error carrying
// every field message.
func (v *Validator) Err() error {
	if len(v.fields) == 0 {
		return nil
	}
	parts := make([]string, 0, len(v.order))
	for _, f := range v.order {
		parts = append(parts, f+" "+v.fields[f])
	}
	sort.Strings(parts)
	return &Error{
		Code:    CodeInvalid,
		Message: strings.Join(parts, "; "),
		Fields:  v.fields,
	}
}

// OK reports whether nothing has failed yet.
func (v *Validator) OK() bool { return len(v.fields) == 0 }
