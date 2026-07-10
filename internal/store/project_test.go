package store_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/px0-ai/px0/internal/store"
	"github.com/px0-ai/px0/internal/testutil"
)

func TestCreateProject(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	tm, err := store.CreateTeam(ctx, "Test Team")
	require.NoError(t, err)

	p, err := store.CreateProject(ctx, tm.ID, "evals", "Evals")
	require.NoError(t, err)
	assert.NotEmpty(t, p.ID)
	assert.Equal(t, tm.ID, p.OwningTeamID)
	assert.Equal(t, "evals", p.Slug)
	assert.Equal(t, "Evals", p.Name)
	assert.NotZero(t, p.CreatedAt)
}

func TestCreateProject_Duplicate(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	tm, err := store.CreateTeam(ctx, "Test Team")
	require.NoError(t, err)

	_, err = store.CreateProject(ctx, tm.ID, "evals", "Evals")
	require.NoError(t, err)

	// Duplicate name within the same owning team.
	_, err = store.CreateProject(ctx, tm.ID, "other_slug", "Evals")
	assert.ErrorIs(t, err, store.ErrDuplicate)

	// Duplicate slug within the same owning team.
	_, err = store.CreateProject(ctx, tm.ID, "evals", "Other Name")
	assert.ErrorIs(t, err, store.ErrDuplicate)

	// Same name/slug under a different owning team is allowed.
	other, err := store.CreateTeam(ctx, "Other Team")
	require.NoError(t, err)
	_, err = store.CreateProject(ctx, other.ID, "evals", "Evals")
	require.NoError(t, err)
}

func TestGetProjectByID(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	tm, err := store.CreateTeam(ctx, "Test Team")
	require.NoError(t, err)

	created, err := store.CreateProject(ctx, tm.ID, "find_me", "Find Me")
	require.NoError(t, err)

	got, err := store.GetProjectByID(ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, created.ID, got.ID)
	assert.Equal(t, tm.ID, got.OwningTeamID)
	assert.Equal(t, "find_me", got.Slug)
}

func TestGetProjectByID_NotFound(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	_, err := store.GetProjectByID(ctx, nonExistentUUID())
	assert.ErrorIs(t, err, store.ErrNotFound)
}

func TestListProjectsForTeam(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	tm, err := store.CreateTeam(ctx, "Owner Team")
	require.NoError(t, err)

	_, err = store.CreateProject(ctx, tm.ID, "proj_a", "Proj A")
	require.NoError(t, err)
	_, err = store.CreateProject(ctx, tm.ID, "proj_b", "Proj B")
	require.NoError(t, err)

	projects, err := store.ListProjectsForTeam(ctx, tm.ID)
	require.NoError(t, err)
	assert.Len(t, projects, 2)

	// A team owning nothing sees an empty list.
	empty, err := store.CreateTeam(ctx, "Empty Team")
	require.NoError(t, err)
	projects, err = store.ListProjectsForTeam(ctx, empty.ID)
	require.NoError(t, err)
	assert.Empty(t, projects)
}

func TestGrantProjectAccess(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	owner, err := store.CreateTeam(ctx, "Owner Team")
	require.NoError(t, err)
	grantee, err := store.CreateTeam(ctx, "Grantee Team")
	require.NoError(t, err)
	project, err := store.CreateProject(ctx, owner.ID, "shared", "Shared")
	require.NoError(t, err)

	require.NoError(t, store.GrantProjectAccess(ctx, project.ID, grantee.ID))

	// Re-granting is a no-op, not an error.
	require.NoError(t, store.GrantProjectAccess(ctx, project.ID, grantee.ID))

	accessible, err := store.IsProjectAccessibleByTeams(ctx, project.ID, []uuid.UUID{grantee.ID})
	require.NoError(t, err)
	assert.True(t, accessible)

	// The grantee now sees the project in its list.
	projects, err := store.ListProjectsForTeam(ctx, grantee.ID)
	require.NoError(t, err)
	assert.Len(t, projects, 1)
}

func TestGrantProjectAccess_UnknownProjectOrTeam(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	owner, err := store.CreateTeam(ctx, "Owner Team")
	require.NoError(t, err)
	project, err := store.CreateProject(ctx, owner.ID, "shared", "Shared")
	require.NoError(t, err)

	err = store.GrantProjectAccess(ctx, nonExistentUUID(), owner.ID)
	assert.ErrorIs(t, err, store.ErrNotFound)

	err = store.GrantProjectAccess(ctx, project.ID, nonExistentUUID())
	assert.ErrorIs(t, err, store.ErrNotFound)
}

func TestRevokeProjectAccess(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	owner, err := store.CreateTeam(ctx, "Owner Team")
	require.NoError(t, err)
	grantee, err := store.CreateTeam(ctx, "Grantee Team")
	require.NoError(t, err)
	project, err := store.CreateProject(ctx, owner.ID, "shared", "Shared")
	require.NoError(t, err)

	require.NoError(t, store.GrantProjectAccess(ctx, project.ID, grantee.ID))
	require.NoError(t, store.RevokeProjectAccess(ctx, project.ID, grantee.ID))

	accessible, err := store.IsProjectAccessibleByTeams(ctx, project.ID, []uuid.UUID{grantee.ID})
	require.NoError(t, err)
	assert.False(t, accessible)
}

func TestRevokeProjectAccess_NotFound(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	owner, err := store.CreateTeam(ctx, "Owner Team")
	require.NoError(t, err)
	grantee, err := store.CreateTeam(ctx, "Grantee Team")
	require.NoError(t, err)
	project, err := store.CreateProject(ctx, owner.ID, "shared", "Shared")
	require.NoError(t, err)

	err = store.RevokeProjectAccess(ctx, project.ID, grantee.ID)
	assert.ErrorIs(t, err, store.ErrNotFound)
}

func TestDeleteProject(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	tm, err := store.CreateTeam(ctx, "Test Team")
	require.NoError(t, err)

	p, err := store.CreateProject(ctx, tm.ID, "delete_me", "Delete Me")
	require.NoError(t, err)

	require.NoError(t, store.DeleteProject(ctx, p.ID))

	_, err = store.GetProjectByID(ctx, p.ID)
	assert.ErrorIs(t, err, store.ErrNotFound)
}

func TestDeleteProject_NotFound(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	err := store.DeleteProject(ctx, nonExistentUUID())
	assert.ErrorIs(t, err, store.ErrNotFound)
}

func TestDeleteTeam_CascadesProjects(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	tm, err := store.CreateTeam(ctx, "Doomed Team")
	require.NoError(t, err)

	p, err := store.CreateProject(ctx, tm.ID, "orphan", "Orphan")
	require.NoError(t, err)

	require.NoError(t, store.DeleteTeam(ctx, tm.ID))

	_, err = store.GetProjectByID(ctx, p.ID)
	assert.ErrorIs(t, err, store.ErrNotFound)
}
