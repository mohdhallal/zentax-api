package domain

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
)

// DocumentRequirement describes a document a task requires.
type DocumentRequirement struct {
	Name        string  `json:"name"                  validate:"required,max=200"`
	Description *string `json:"description,omitempty" validate:"omitempty,max=1000"`
	Required    bool    `json:"required"`
}

// DocumentRequirements is a JSONB-stored list of document requirements.
type DocumentRequirements []DocumentRequirement

func (d DocumentRequirements) Value() (driver.Value, error) {
	if d == nil {
		return []byte("[]"), nil
	}
	return json.Marshal([]DocumentRequirement(d))
}

func (d *DocumentRequirements) Scan(src any) error {
	var data []byte
	switch v := src.(type) {
	case nil:
		*d = DocumentRequirements{}
		return nil
	case []byte:
		data = v
	case string:
		data = []byte(v)
	default:
		return fmt.Errorf("DocumentRequirements.Scan: unsupported source type %T", src)
	}

	if len(data) == 0 {
		*d = DocumentRequirements{}
		return nil
	}
	var out []DocumentRequirement
	if err := json.Unmarshal(data, &out); err != nil {
		return fmt.Errorf("DocumentRequirements.Scan: invalid JSON: %w", err)
	}
	*d = out
	return nil
}
