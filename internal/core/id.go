package core

import (
	"crypto/rand"
	"database/sql/driver"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"

	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

// ID is a RFC 4122 version 4 UUID. Every primary key in the system is one.
// It is its own type so that a trip id can never be silently passed where a
// member id is expected without an explicit conversion.
type ID [16]byte

// Nil is the zero UUID, used to mean "absent" for non-pointer fields.
var Nil ID

// NewID returns a cryptographically random UUID v4.
func NewID() ID {
	var id ID
	if _, err := rand.Read(id[:]); err != nil {
		panic("core: entropy source failed: " + err.Error())
	}
	id[6] = (id[6] & 0x0f) | 0x40 // version 4
	id[8] = (id[8] & 0x3f) | 0x80 // variant RFC 4122
	return id
}

func (id ID) IsZero() bool { return id == Nil }

func (id ID) String() string {
	var buf [36]byte
	hex.Encode(buf[0:8], id[0:4])
	buf[8] = '-'
	hex.Encode(buf[9:13], id[4:6])
	buf[13] = '-'
	hex.Encode(buf[14:18], id[6:8])
	buf[18] = '-'
	hex.Encode(buf[19:23], id[8:10])
	buf[23] = '-'
	hex.Encode(buf[24:36], id[10:16])
	return string(buf[:])
}

// ParseID accepts the canonical dashed form and the bare 32 hex digit form.
func ParseID(s string) (ID, error) {
	var id ID
	switch len(s) {
	case 36:
		if s[8] != '-' || s[13] != '-' || s[18] != '-' || s[23] != '-' {
			return Nil, fmt.Errorf("core: malformed uuid %q", s)
		}
		compact := s[0:8] + s[9:13] + s[14:18] + s[19:23] + s[24:36]
		if _, err := hex.Decode(id[:], []byte(compact)); err != nil {
			return Nil, fmt.Errorf("core: malformed uuid %q", s)
		}
	case 32:
		if _, err := hex.Decode(id[:], []byte(s)); err != nil {
			return Nil, fmt.Errorf("core: malformed uuid %q", s)
		}
	default:
		return Nil, fmt.Errorf("core: malformed uuid %q", s)
	}
	return id, nil
}

// MustParseID is for tests and constants.
func MustParseID(s string) ID {
	id, err := ParseID(s)
	if err != nil {
		panic(err)
	}
	return id
}

func (id ID) MarshalText() ([]byte, error) { return []byte(id.String()), nil }

func (id *ID) UnmarshalText(b []byte) error {
	parsed, err := ParseID(string(b))
	if err != nil {
		return err
	}
	*id = parsed
	return nil
}

// Value implements driver.Valuer. The zero ID is written as SQL NULL, which
// is what lets an optional foreign key be a plain ID field rather than a
// pointer everywhere.
func (id ID) Value() (driver.Value, error) {
	if id.IsZero() {
		return nil, nil
	}
	return id.String(), nil
}

// GormDataType keeps ids as fixed width text on every dialect. PostgreSQL has
// a native uuid type and SQLite does not, and one portable representation is
// worth more here than the few bytes the native type would save.
func (ID) GormDataType() string { return "string" }

// GormDBDataType is the concrete column type per dialect.
func (ID) GormDBDataType(*gorm.DB, *schema.Field) string { return "varchar(36)" }

// Scan implements sql.Scanner. Drivers hand a uuid column over as text, as
// raw bytes, or as a 16 byte array depending on dialect and result format.
func (id *ID) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*id = Nil
		return nil
	case [16]byte:
		*id = ID(v)
		return nil
	case []byte:
		if len(v) == 16 {
			copy(id[:], v)
			return nil
		}
		return id.UnmarshalText(v)
	case string:
		return id.UnmarshalText([]byte(v))
	default:
		return errors.New("core: cannot scan uuid")
	}
}

// Compact renders the id as 22 URL-safe characters. Telegram caps
// callback_data at 64 bytes, which is not enough for two dashed UUIDs, so
// callback payloads use this form.
func (id ID) Compact() string { return base64.RawURLEncoding.EncodeToString(id[:]) }

// ParseCompactID reads the 22 character form produced by Compact. It also
// accepts the canonical dashed form so callers never have to care which one
// they were handed.
func ParseCompactID(s string) (ID, error) {
	if len(s) != 22 {
		return ParseID(s)
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || len(raw) != 16 {
		return Nil, fmt.Errorf("core: malformed compact id %q", s)
	}
	var id ID
	copy(id[:], raw)
	return id, nil
}

// IDStrings renders ids for an SQL IN clause. Neither supported engine takes a
// typed array parameter portably, so lists always travel as plain strings.
func IDStrings(ids []ID) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, id.String())
	}
	return out
}
