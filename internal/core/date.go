package core

import (
	"database/sql/driver"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

// DateLayout is the only date format the API speaks.
const DateLayout = "2006-01-02"

// Date is a calendar date with no time and no zone. Trip start and end are
// dates, not instants: "23 September" means the same thing to everyone on the
// trip regardless of where they read it from.
type Date struct {
	Year  int
	Month time.Month
	Day   int
}

// NewDate builds a Date from its parts.
func NewDate(year int, month time.Month, day int) Date {
	return Date{Year: year, Month: month, Day: day}
}

// DateOf truncates an instant to the calendar date it falls on in loc.
func DateOf(t time.Time, loc *time.Location) Date {
	if loc != nil {
		t = t.In(loc)
	}
	y, m, d := t.Date()
	return Date{Year: y, Month: m, Day: d}
}

// ParseDate reads "2006-01-02".
func ParseDate(s string) (Date, error) {
	t, err := time.Parse(DateLayout, strings.TrimSpace(s))
	if err != nil {
		return Date{}, fmt.Errorf("core: %q is not a YYYY-MM-DD date", s)
	}
	return DateOf(t, time.UTC), nil
}

func (d Date) IsZero() bool { return d == Date{} }

func (d Date) String() string {
	return fmt.Sprintf("%04d-%02d-%02d", d.Year, int(d.Month), d.Day)
}

// In returns midnight of this date in loc.
func (d Date) In(loc *time.Location) time.Time {
	if loc == nil {
		loc = time.UTC
	}
	return time.Date(d.Year, d.Month, d.Day, 0, 0, 0, 0, loc)
}

// EndOfDayIn returns the first instant of the following day in loc, which is
// the exclusive upper bound of the date.
func (d Date) EndOfDayIn(loc *time.Location) time.Time { return d.AddDays(1).In(loc) }

// AddDays shifts the date by n days.
func (d Date) AddDays(n int) Date { return DateOf(d.In(time.UTC).AddDate(0, 0, n), time.UTC) }

// Before reports whether d is earlier than other.
func (d Date) Before(other Date) bool { return d.In(time.UTC).Before(other.In(time.UTC)) }

// After reports whether d is later than other.
func (d Date) After(other Date) bool { return other.Before(d) }

// DaysUntil counts whole days from d to other, negative when other is earlier.
func (d Date) DaysUntil(other Date) int {
	return int(other.In(time.UTC).Sub(d.In(time.UTC)).Hours() / 24)
}

// Nights is the number of overnight stays a start..end range implies.
func (d Date) Nights(end Date) int {
	n := d.DaysUntil(end)
	if n < 0 {
		return 0
	}
	return n
}

func (d Date) MarshalText() ([]byte, error) { return []byte(d.String()), nil }

func (d *Date) UnmarshalText(b []byte) error {
	parsed, err := ParseDate(string(b))
	if err != nil {
		return err
	}
	*d = parsed
	return nil
}

// Value writes the date as YYYY-MM-DD text. SQLite has no date type and
// PostgreSQL parses the literal happily, so text is the portable choice and it
// makes the stored value readable in a console.
func (d Date) Value() (driver.Value, error) {
	if d.IsZero() {
		return nil, nil
	}
	return d.String(), nil
}

// GormDataType keeps dates as text on every dialect.
func (Date) GormDataType() string { return "string" }

// GormDBDataType is the concrete column type per dialect.
func (Date) GormDBDataType(*gorm.DB, *schema.Field) string { return "varchar(10)" }

// Scan reads a SQL date column.
func (d *Date) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*d = Date{}
		return nil
	case time.Time:
		*d = DateOf(v, time.UTC)
		return nil
	case int64:
		*d = DateOf(time.Unix(v, 0), time.UTC)
		return nil
	case string:
		return d.UnmarshalText([]byte(v))
	case []byte:
		return d.UnmarshalText(v)
	default:
		return fmt.Errorf("core: cannot scan %T into Date", src)
	}
}

// LoadLocation resolves an IANA timezone name, falling back to UTC so a bad
// value in the database can never take a request down.
func LoadLocation(name string) *time.Location {
	if strings.TrimSpace(name) == "" {
		return time.UTC
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return time.UTC
	}
	return loc
}

// IsValidTimezone reports whether name is a known IANA zone.
func IsValidTimezone(name string) bool {
	_, err := time.LoadLocation(strings.TrimSpace(name))
	return err == nil
}
