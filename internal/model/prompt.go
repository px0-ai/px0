package model

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

const (
	PromptStatusActive   = "active"
	PromptStatusArchived = "archived"
)

type Prompt struct {
	ID          uuid.UUID `json:"id"`
	TeamID      uuid.UUID `json:"team_id"`
	Slug        string    `json:"slug"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

const (
	VersionStatusDraft    = "draft"
	VersionStatusStable   = "stable"
	VersionStatusLive     = "live"
	VersionStatusArchived = "archived"
)

type PromptVersion struct {
	ID          uuid.UUID       `json:"id"`
	PromptID    uuid.UUID       `json:"prompt_id"`
	Version     int             `json:"version"`
	Template    string          `json:"template"`
	Status      string          `json:"status"`
	Model       *string         `json:"model"`
	ModelParams json.RawMessage `json:"model_params"`
	CreatedAt   time.Time       `json:"created_at"`
	PublishedAt *time.Time      `json:"published_at"`
	Tags        []string        `json:"tags"`
}
