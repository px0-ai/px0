package model

import (
	"time"

	"github.com/google/uuid"
)

type Prompt struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

const (
	VersionStatusDraft    = "draft"
	VersionStatusLive     = "live"
	VersionStatusArchived = "archived"
)

type PromptVersion struct {
	ID          uuid.UUID  `json:"id"`
	PromptID    uuid.UUID  `json:"prompt_id"`
	Version     int        `json:"version"`
	Template    string     `json:"template"`
	Status      string     `json:"status"`
	CreatedAt   time.Time  `json:"created_at"`
	PublishedAt *time.Time `json:"published_at"`
}
