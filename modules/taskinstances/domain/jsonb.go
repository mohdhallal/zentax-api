package domain

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
)

// TaxData is the schema-driven per-instance tax data, stored as JSONB (nullable).
type TaxData map[string]any

func (t TaxData) Value() (driver.Value, error) {
	if t == nil {
		return nil, nil
	}
	return json.Marshal(map[string]any(t))
}

func (t *TaxData) Scan(src any) error {
	var data []byte
	switch v := src.(type) {
	case nil:
		*t = nil
		return nil
	case []byte:
		data = v
	case string:
		data = []byte(v)
	default:
		return fmt.Errorf("TaxData.Scan: unsupported source type %T", src)
	}

	if len(data) == 0 {
		*t = nil
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return fmt.Errorf("TaxData.Scan: invalid JSON: %w", err)
	}
	*t = out
	return nil
}
