package model

import (
	"time"

	"github.com/google/uuid"
)

type Skill struct {
	ID          uuid.UUID `json:"id"`
	ProjectID   uuid.UUID `json:"project_id"`
	Slug        string    `json:"slug"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type SkillVersion struct {
	ID        uuid.UUID `json:"id"`
	SkillID   uuid.UUID `json:"skill_id"`
	Version   int       `json:"version"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type SkillFile struct {
	ID        uuid.UUID `json:"id"`
	SkillID   uuid.UUID `json:"skill_id"`
	VersionID uuid.UUID `json:"version_id"`
	FilePath  string    `json:"file_path"`
	Content   []byte    `json:"-"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
