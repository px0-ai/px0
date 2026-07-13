package model

import (
	"time"

	"github.com/google/uuid"
)

// Project is a named container for prompts owned by exactly one team.
type Project struct {
	ID           uuid.UUID `json:"id"`
	OwningTeamID uuid.UUID `json:"owning_team_id"`
	Name         string    `json:"name"`
	Slug         string    `json:"slug"`
	CreatedAt    time.Time `json:"created_at"`
}
