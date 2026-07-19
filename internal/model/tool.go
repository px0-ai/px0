package model

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type Tool struct {
	ID          uuid.UUID `json:"id"`
	ProjectID   uuid.UUID `json:"project_id"`
	Slug        string    `json:"slug"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	URL         string    `json:"url"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type ToolVersion struct {
	ID           uuid.UUID       `json:"id"`
	ToolID       uuid.UUID       `json:"tool_id"`
	Version      int             `json:"version"`
	InputSchema  json.RawMessage `json:"input_schema"`
	OutputSchema json.RawMessage `json:"output_schema"`
	Status       string          `json:"status"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
}

type ToolInvocation struct {
	ID              int64            `json:"id"`
	ToolID          uuid.UUID        `json:"tool_id"`
	ToolVersion     int              `json:"tool_version"`
	RequestPayload  json.RawMessage  `json:"request_payload"`
	ResponsePayload *json.RawMessage `json:"response_payload,omitempty"`
	Error           *string          `json:"error,omitempty"`
	StatusCode      *int             `json:"status_code,omitempty"`
	CreatedAt       time.Time        `json:"created_at"`
}
