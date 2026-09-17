package core

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"

	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

// JSONMap is a small bag of structured metadata stored as a JSON text column.
//
// It is text rather than PostgreSQL's jsonb on purpose: nothing in this
// product queries inside these blobs, they are read back whole and handed to a
// client, and text is the one representation both supported engines agree on.
type JSONMap map[string]any

// Value marshals the map, writing NULL for an empty one.
func (m JSONMap) Value() (driver.Value, error) {
	if len(m) == 0 {
		return nil, nil
	}
	buf, err := json.Marshal(map[string]any(m))
	if err != nil {
		return nil, fmt.Errorf("core: encode json column: %w", err)
	}
	return string(buf), nil
}

// Scan unmarshals a text or bytes column.
func (m *JSONMap) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*m = nil
		return nil
	case []byte:
		if len(v) == 0 {
			*m = nil
			return nil
		}
		return json.Unmarshal(v, (*map[string]any)(m))
	case string:
		if v == "" {
			*m = nil
			return nil
		}
		return json.Unmarshal([]byte(v), (*map[string]any)(m))
	default:
		return fmt.Errorf("core: cannot scan %T into JSONMap", src)
	}
}

func (JSONMap) GormDataType() string { return "string" }

func (JSONMap) GormDBDataType(*gorm.DB, *schema.Field) string { return "text" }
