package model

import (
	"time"

	"github.com/google/uuid"
)

type APIKey struct {
	ID         uuid.UUID  `json:"id"`
	Name       string     `json:"name"`
	OrgID      uuid.UUID  `json:"org_id"`
	TeamID     *uuid.UUID `json:"team_id,omitempty"`
	KeyPrefix  string     `json:"key_prefix"`
	KeyHash    string     `json:"-"`
	Operation  string     `json:"operation"` // "read_render" or "all"
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
}
