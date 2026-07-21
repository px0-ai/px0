package model

import (
	"time"

	"github.com/google/uuid"
)

type SearchEntityType string

const (
	SearchEntityPrompt SearchEntityType = "prompt"
	SearchEntitySkill  SearchEntityType = "skill"
	SearchEntityTool   SearchEntityType = "tool"
)

func AllSearchEntityTypes() []SearchEntityType {
	return []SearchEntityType{SearchEntityPrompt, SearchEntitySkill, SearchEntityTool}
}

type SearchReference struct {
	Type SearchEntityType
	ID   uuid.UUID
}

// SearchResult is the common metadata shared by every searchable registry
// entity. Type identifies the canonical endpoint that owns the full resource.
type SearchResult struct {
	Type        SearchEntityType `json:"type"`
	ID          uuid.UUID        `json:"id"`
	ProjectID   uuid.UUID        `json:"project_id"`
	Slug        string           `json:"slug"`
	Name        string           `json:"name"`
	Description string           `json:"description"`
	CreatedAt   time.Time        `json:"created_at"`
	UpdatedAt   time.Time        `json:"updated_at"`
}
