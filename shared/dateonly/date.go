// Package dateonly implements ADR-0002: legal/business dates are timezone-
// agnostic calendar dates, handled as YYYY-MM-DD from database to screen and
// never converted through a timezone.
package dateonly

import (
	"database/sql/driver"
	"fmt"
	"strings"
	"time"
)

// Date is a timezone-agnostic calendar date (year, month, day). It stores as a
// Postgres `date`, transports as a YYYY-MM-DD JSON string, and never passes
// through a timezone conversion.
type Date struct {
	Year  int
	Month int // 1-12
	Day   int
}

const layout = "2006-01-02"

func New(year, month, day int) Date {
	return Date{Year: year, Month: month, Day: day}
}

// FromTime takes the calendar date of t as read in t's own location.
func FromTime(t time.Time) Date {
	y, m, d := t.Date()
	return Date{Year: y, Month: int(m), Day: d}
}

// Time returns midnight UTC of the date. For calendar arithmetic only — never
// render this through a local timezone.
func (d Date) Time() time.Time {
	return time.Date(d.Year, time.Month(d.Month), d.Day, 0, 0, 0, 0, time.UTC)
}

func (d Date) String() string {
	return fmt.Sprintf("%04d-%02d-%02d", d.Year, d.Month, d.Day)
}

func (d Date) IsZero() bool {
	return d == Date{}
}

// Parse reads a YYYY-MM-DD string (extra time suffix, if any, is ignored).
func Parse(s string) (Date, error) {
	if len(s) < 10 {
		return Date{}, fmt.Errorf("dateonly: invalid date %q (want YYYY-MM-DD)", s)
	}
	t, err := time.Parse(layout, s[:10])
	if err != nil {
		return Date{}, fmt.Errorf("dateonly: invalid date %q (want YYYY-MM-DD)", s)
	}
	return FromTime(t), nil
}

func (d Date) MarshalJSON() ([]byte, error) {
	return []byte(`"` + d.String() + `"`), nil
}

func (d *Date) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		*d = Date{}
		return nil
	}
	parsed, err := Parse(s)
	if err != nil {
		return err
	}
	*d = parsed
	return nil
}

// Value renders the date for a Postgres `date` column.
func (d Date) Value() (driver.Value, error) {
	return d.String(), nil
}

// Scan reads a Postgres `date` (pgx returns it as time.Time) or a string.
func (d *Date) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*d = Date{}
		return nil
	case time.Time:
		*d = FromTime(v)
		return nil
	case string:
		parsed, err := Parse(v)
		if err != nil {
			return err
		}
		*d = parsed
		return nil
	case []byte:
		parsed, err := Parse(string(v))
		if err != nil {
			return err
		}
		*d = parsed
		return nil
	default:
		return fmt.Errorf("dateonly: cannot scan %T into Date", src)
	}
}
