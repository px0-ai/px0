package store_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/px0-ai/px0/internal/store"
	"github.com/px0-ai/px0/internal/testutil"
)

func TestCreateAndGetTool(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()

	tm, err := store.CreateTeam(ctx, "Test Team")
	require.NoError(t, err)

	p, err := store.CreateProject(ctx, tm.ID, "tools_project", "Tools Project")
	require.NoError(t, err)

	tool, err := store.CreateTool(ctx, p.ID, "my-tool", "My Tool", "A test tool", "")
	require.NoError(t, err)
	assert.NotEmpty(t, tool.ID)
	assert.Equal(t, p.ID, tool.ProjectID)
	assert.Equal(t, "my-tool", tool.Slug)
	assert.Equal(t, "My Tool", tool.Name)
	assert.Equal(t, "A test tool", tool.Description)

	// Retrieve tool by ID
	t2, err := store.GetToolByID(ctx, tool.ID, []uuid.UUID{p.ID})
	require.NoError(t, err)
	assert.Equal(t, tool.ID, t2.ID)

	// Retrieve tool by slug
	t3, err := store.GetToolBySlug(ctx, "my-tool", []uuid.UUID{p.ID})
	require.NoError(t, err)
	assert.Equal(t, tool.ID, t3.ID)

	// Get with unauthorized project ID should return NotFound
	_, err = store.GetToolByID(ctx, tool.ID, []uuid.UUID{uuid.New()})
	assert.ErrorIs(t, err, store.ErrNotFound)
}

func TestCreateTool_Duplicate(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()

	tm, err := store.CreateTeam(ctx, "Test Team")
	require.NoError(t, err)

	p, err := store.CreateProject(ctx, tm.ID, "tools_project", "Tools Project")
	require.NoError(t, err)

	_, err = store.CreateTool(ctx, p.ID, "my-tool", "My Tool", "A test tool", "")
	require.NoError(t, err)

	// Duplicate slug
	_, err = store.CreateTool(ctx, p.ID, "my-tool", "Other Tool", "", "")
	assert.ErrorIs(t, err, store.ErrDuplicate)

	// Duplicate name
	_, err = store.CreateTool(ctx, p.ID, "other-tool", "My Tool", "", "")
	assert.ErrorIs(t, err, store.ErrDuplicate)
}

func TestUpdateAndDeleteTool(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()

	tm, err := store.CreateTeam(ctx, "Test Team")
	require.NoError(t, err)

	p, err := store.CreateProject(ctx, tm.ID, "tools_project", "Tools Project")
	require.NoError(t, err)

	tool, err := store.CreateTool(ctx, p.ID, "my-tool", "My Tool", "A test tool", "http://original.url")
	require.NoError(t, err)

	// Update metadata
	updated, err := store.UpdateTool(ctx, tool.ID, []uuid.UUID{p.ID}, "my-tool-updated", "My Tool Updated", "New Description", "http://updated.url")
	require.NoError(t, err)
	assert.Equal(t, "my-tool-updated", updated.Slug)
	assert.Equal(t, "My Tool Updated", updated.Name)
	assert.Equal(t, "New Description", updated.Description)
	assert.Equal(t, "http://updated.url", updated.URL)

	// Delete
	err = store.DeleteTool(ctx, tool.ID, []uuid.UUID{p.ID})
	require.NoError(t, err)

	_, err = store.GetToolByID(ctx, tool.ID, []uuid.UUID{p.ID})
	assert.ErrorIs(t, err, store.ErrNotFound)
}

func TestToolVersions(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()

	tm, err := store.CreateTeam(ctx, "Test Team")
	require.NoError(t, err)

	p, err := store.CreateProject(ctx, tm.ID, "tools_project", "Tools Project")
	require.NoError(t, err)

	tool, err := store.CreateTool(ctx, p.ID, "my-tool", "My Tool", "A test tool", "")
	require.NoError(t, err)

	input := json.RawMessage(`{"type": "object", "properties": {"query": {"type": "string"}}}`)
	output := json.RawMessage(`{"type": "string"}`)

	v1, err := store.CreateToolVersion(ctx, tool.ID, input, output)
	require.NoError(t, err)
	assert.Equal(t, 1, v1.Version)
	assert.Equal(t, "draft", v1.Status)

	// Test update version
	newInput := json.RawMessage(`{"type": "object", "properties": {"query": {"type": "string"}, "limit": {"type": "integer"}}}`)
	v1Updated, err := store.UpdateToolVersion(ctx, v1.ID, newInput, output)
	require.NoError(t, err)
	assert.JSONEq(t, string(newInput), string(v1Updated.InputSchema))

	// Test promote draft to stable
	v1Stable, err := store.PromoteToolVersion(ctx, tool.ID, 1)
	require.NoError(t, err)
	assert.Equal(t, "stable", v1Stable.Status)

	// Update on stable should fail since only drafts can be updated
	_, err = store.UpdateToolVersion(ctx, v1.ID, input, output)
	assert.Error(t, err)

	// Promote stable to live
	v1Live, err := store.PromoteToolVersion(ctx, tool.ID, 1)
	require.NoError(t, err)
	assert.Equal(t, "live", v1Live.Status)

	// Create v2
	v2, err := store.CreateToolVersion(ctx, tool.ID, input, output)
	require.NoError(t, err)
	assert.Equal(t, 2, v2.Version)
	assert.Equal(t, "draft", v2.Status)

	// Duplicate v1 (which is live)
	v3, err := store.DuplicateToolVersion(ctx, tool.ID, 1)
	require.NoError(t, err)
	assert.Equal(t, 3, v3.Version)
	assert.Equal(t, "draft", v3.Status)
	assert.JSONEq(t, string(newInput), string(v3.InputSchema)) // copied from v1's schema

	// Demote v1
	v1Demoted, err := store.DemoteToolVersion(ctx, tool.ID, 1)
	require.NoError(t, err)
	assert.Equal(t, "stable", v1Demoted.Status)

	// Archive v1
	v1Archived, err := store.ArchiveToolVersion(ctx, tool.ID, 1)
	require.NoError(t, err)
	assert.Equal(t, "archived", v1Archived.Status)

	// Delete v2 (draft) -> should actually delete from DB
	err = store.DeleteToolVersion(ctx, tool.ID, 2)
	require.NoError(t, err)

	_, err = store.GetToolVersion(ctx, tool.ID, 2)
	assert.ErrorIs(t, err, store.ErrNotFound)

	// Delete v3 (draft)
	err = store.DeleteToolVersion(ctx, tool.ID, 3)
	require.NoError(t, err)

	// Delete v1 (archived) -> should just remain archived or stay as archived
	err = store.DeleteToolVersion(ctx, tool.ID, 1)
	require.NoError(t, err)
	v1Final, err := store.GetToolVersion(ctx, tool.ID, 1)
	require.NoError(t, err)
	assert.Equal(t, "archived", v1Final.Status)
}

func TestToolInvocations(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()

	tm, err := store.CreateTeam(ctx, "Test Team")
	require.NoError(t, err)

	p, err := store.CreateProject(ctx, tm.ID, "tools_project", "Tools Project")
	require.NoError(t, err)

	tool, err := store.CreateTool(ctx, p.ID, "my-tool", "My Tool", "A test tool", "http://test.url")
	require.NoError(t, err)

	reqRaw := json.RawMessage(`{"param1": "value1"}`)
	respRaw := json.RawMessage(`{"result": "success"}`)
	errText := "Some error details"
	statusVal := 200

	// 1. Log an invocation
	inv1, err := store.LogToolInvocation(ctx, tool.ID, 1, reqRaw, &respRaw, nil, &statusVal)
	require.NoError(t, err)
	assert.NotEmpty(t, inv1.ID)
	assert.Equal(t, tool.ID, inv1.ToolID)
	assert.Equal(t, 1, inv1.ToolVersion)
	assert.JSONEq(t, `{"param1": "value1"}`, string(inv1.RequestPayload))
	assert.JSONEq(t, `{"result": "success"}`, string(*inv1.ResponsePayload))
	assert.Nil(t, inv1.Error)
	assert.Equal(t, statusVal, *inv1.StatusCode)

	// Log a second invocation with an error
	statusVal2 := 500
	inv2, err := store.LogToolInvocation(ctx, tool.ID, 1, reqRaw, nil, &errText, &statusVal2)
	require.NoError(t, err)
	assert.NotEmpty(t, inv2.ID)
	assert.Equal(t, tool.ID, inv2.ToolID)
	assert.Equal(t, 1, inv2.ToolVersion)
	assert.JSONEq(t, `{"param1": "value1"}`, string(inv2.RequestPayload))
	assert.Nil(t, inv2.ResponsePayload)
	assert.Equal(t, errText, *inv2.Error)
	assert.Equal(t, statusVal2, *inv2.StatusCode)

	// 2. List invocations with default limit
	invList, err := store.ListToolInvocations(ctx, tool.ID, 0, 10)
	require.NoError(t, err)
	require.Len(t, invList, 2)
	// Ordered newest to oldest
	assert.Equal(t, inv2.ID, invList[0].ID)
	assert.Equal(t, inv1.ID, invList[1].ID)

	// 3. Test cursor-based pagination
	invListCursor, err := store.ListToolInvocations(ctx, tool.ID, inv2.ID, 1)
	require.NoError(t, err)
	require.Len(t, invListCursor, 1)
	assert.Equal(t, inv1.ID, invListCursor[0].ID)
}
