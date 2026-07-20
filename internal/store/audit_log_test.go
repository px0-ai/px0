package store_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/px0-ai/px0/internal/store"
	"github.com/px0-ai/px0/internal/testutil"
)

func TestAuditLogStore(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()

	org, err := store.CreateOrganization(ctx, "Audit Log Org")
	require.NoError(t, err)

	err = store.InsertAuditLog(ctx, org.ID, nil, "CREATE", "project", nil, map[string]any{"key": "value"})
	require.NoError(t, err)

	err = store.InsertAuditLog(ctx, org.ID, nil, "DELETE", "project", nil, nil)
	require.NoError(t, err)

	logs, err := store.ListAuditLogsForOrg(ctx, org.ID, 10, 0)
	require.NoError(t, err)
	require.Len(t, logs, 2)
	assert.Equal(t, "DELETE", logs[0].Action) // Descending order
	assert.Equal(t, "CREATE", logs[1].Action)
	assert.Equal(t, "value", logs[1].Metadata["key"])
}
