package model

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type PromptPayload struct {
	ID        uuid.UUID       `json:"id"`
	PromptID  uuid.UUID       `json:"prompt_id"`
	Name      *string         `json:"name"`
	Variables json.RawMessage `json:"variables"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}
