package core

import "time"

// Every instant in this system is stored in UTC. The trip's timezone is a
// separate column and conversion happens when something is displayed.
//
// Normalising is not cosmetic. Clients send times with whatever offset their
// phone is in, and the storage layer compares instants as written: SQLite
// compares the text, and a PostgreSQL `timestamp` column drops the offset
// without converting. An unnormalised "12:09+02:00" therefore sorts and ranges
// as though it were 12:09 UTC, which silently moves it two hours and makes
// every deadline and reminder window wrong.
//
// Services call these on every instant they accept from outside.

// UTC returns t in UTC, leaving the zero time alone so it stays recognisable
// as "unset".
func UTC(t time.Time) time.Time {
	if t.IsZero() {
		return t
	}
	return t.UTC()
}

// UTCPtr returns an optional instant in UTC, preserving nil.
func UTCPtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	utc := UTC(*t)
	return &utc
}
