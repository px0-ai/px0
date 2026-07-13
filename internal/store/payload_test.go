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

func TestPromptPayloadCRUD(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()

	proj := newProject(t, ctx, "Test")

	p, err := store.CreatePrompt(ctx, proj.ID, "greeting", "Greeting", "A greeting prompt")
	require.NoError(t, err)

	// 1. Create Payload
	vars := []byte(`{"user":"Arpit","role":"Admin"}`)
	payload, err := store.CreatePromptPayload(ctx, p.ID, vars)
	require.NoError(t, err)
	assert.NotEmpty(t, payload.ID)
	assert.Equal(t, p.ID, payload.PromptID)
	assert.Nil(t, payload.Name) // Default is nil
	assert.JSONEq(t, string(vars), string(payload.Variables))

	// 2. Get Payload
	got, err := store.GetPromptPayload(ctx, payload.ID, p.ID)
	require.NoError(t, err)
	assert.Equal(t, payload.ID, got.ID)
	assert.Nil(t, got.Name)
	assert.JSONEq(t, string(vars), string(got.Variables))

	// Get with non-existent ID or mismatched prompt ID should return ErrNotFound
	_, err = store.GetPromptPayload(ctx, uuid.New(), p.ID)
	assert.ErrorIs(t, err, store.ErrNotFound)

	_, err = store.GetPromptPayload(ctx, payload.ID, uuid.New())
	assert.ErrorIs(t, err, store.ErrNotFound)

	// 3. List Payloads
	list, err := store.ListPromptPayloads(ctx, p.ID)
	require.NoError(t, err)
	assert.Len(t, list, 1)
	assert.Equal(t, payload.ID, list[0].ID)

	// List for other prompt ID should return empty
	otherList, err := store.ListPromptPayloads(ctx, uuid.New())
	require.NoError(t, err)
	assert.Empty(t, otherList)

	// 4. Update Payload
	newName := "Admin User Test"
	newVars := []byte(`{"user":"Alice","role":"Editor"}`)
	updated, err := store.UpdatePromptPayload(ctx, payload.ID, p.ID, &newName, newVars)
	require.NoError(t, err)
	assert.Equal(t, payload.ID, updated.ID)
	require.NotNil(t, updated.Name)
	assert.Equal(t, newName, *updated.Name)
	assert.JSONEq(t, string(newVars), string(updated.Variables))

	// Update only name
	anotherName := "Updated Name Only"
	updated2, err := store.UpdatePromptPayload(ctx, payload.ID, p.ID, &anotherName, nil)
	require.NoError(t, err)
	assert.Equal(t, anotherName, *updated2.Name)
	assert.JSONEq(t, string(newVars), string(updated2.Variables))

	// Update only variables
	finalVars := []byte(`{"user":"Bob","role":"Viewer"}`)
	updated3, err := store.UpdatePromptPayload(ctx, payload.ID, p.ID, nil, finalVars)
	require.NoError(t, err)
	assert.Equal(t, anotherName, *updated3.Name)
	assert.JSONEq(t, string(finalVars), string(updated3.Variables))

	// 5. Delete Payload
	err = store.DeletePromptPayload(ctx, payload.ID, p.ID)
	require.NoError(t, err)

	_, err = store.GetPromptPayload(ctx, payload.ID, p.ID)
	assert.ErrorIs(t, err, store.ErrNotFound)

	err = store.DeletePromptPayload(ctx, payload.ID, p.ID)
	assert.ErrorIs(t, err, store.ErrNotFound)
}
