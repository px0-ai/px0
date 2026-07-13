package model

import "github.com/google/uuid"

const (
	RoleViewer = "viewer"
	RoleEditor = "editor"
	RoleAdmin  = "admin"
)

// Subject represents the authenticated principal (a User or an API Key).
type Subject struct {
	UserID         uuid.UUID
	IsUserAdmin    bool
	IsUserVerified bool

	IsAPIKey bool

	// OrgID is the organization scope. For API Keys, it's their assigned org.
	// For Users, it might be the org of their primary team, or nil.
	OrgID      *uuid.UUID
	IsOrgAdmin bool

	// TeamRoles maps team IDs to the subject's granted role in that team.
	// For users, this is their actual team membership role.
	// For API keys, this is computed based on their operation ('read_render', 'all', 'admin')
	// and their team scope.
	TeamRoles map[uuid.UUID]string
}
