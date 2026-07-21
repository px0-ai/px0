package handler_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPurgeCache_Endpoints(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)

	// Global Purge
	req := newReq(t, http.MethodPost, "/v1/cache/purge", "", token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Prompt Purge
	id := uuid.NewString()
	req = newReq(t, http.MethodPost, fmt.Sprintf("/v1/prompts/%s/cache/purge", id), "", token)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Skill Purge
	req = newReq(t, http.MethodPost, fmt.Sprintf("/v1/skills/%s/cache/purge", id), "", token)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Tool Purge
	req = newReq(t, http.MethodPost, fmt.Sprintf("/v1/tools/%s/cache/purge", id), "", token)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}
