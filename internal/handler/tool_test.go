package handler_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/px0-ai/px0/internal/store"
)

func TestToolHandlers(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	projectID := setupProject(t, a, token)

	// 1. Create Tool
	req := newReq(t, http.MethodPost, fmt.Sprintf("/v1/projects/%s/tools", projectID),
		`{"name":"Search Tool","slug":"search-tool","description":"Searches the web"}`, token)
	resp, err := a.App.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)

	body := decodeBody(t, resp)
	tool := body["tool"].(map[string]any)
	toolID := tool["id"].(string)
	assert.NotEmpty(t, toolID)
	assert.Equal(t, "Search Tool", tool["name"])
	assert.Equal(t, "search_tool", tool["slug"]) // slug should be normalized!
	assert.Equal(t, "Searches the web", tool["description"])

	// 2. Create Duplicate Tool (conflict)
	reqDup := newReq(t, http.MethodPost, fmt.Sprintf("/v1/projects/%s/tools", projectID),
		`{"name":"Search Tool","slug":"search-tool"}`, token)
	respDup, err := a.App.Test(reqDup)
	require.NoError(t, err)
	assert.Equal(t, http.StatusConflict, respDup.StatusCode)

	// 3. List Tools in Project
	reqList := newReq(t, http.MethodGet, fmt.Sprintf("/v1/projects/%s/tools", projectID), "", token)
	respList, err := a.App.Test(reqList)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, respList.StatusCode)
	bodyList := decodeBody(t, respList)
	tools := bodyList["tools"].([]any)
	assert.Len(t, tools, 1)

	// 4. List All Tools (global, filtering by project ID)
	reqListAll := newReq(t, http.MethodGet, fmt.Sprintf("/v1/tools?project=%s", projectID), "", token)
	respListAll, err := a.App.Test(reqListAll)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, respListAll.StatusCode)
	bodyListAll := decodeBody(t, respListAll)
	toolsAll := bodyListAll["tools"].([]any)
	assert.Len(t, toolsAll, 1)

	// 5. Get Tool by ID
	reqGet := newReq(t, http.MethodGet, fmt.Sprintf("/v1/tools/%s", toolID), "", token)
	respGet, err := a.App.Test(reqGet)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, respGet.StatusCode)
	bodyGet := decodeBody(t, respGet)
	toolGot := bodyGet["tool"].(map[string]any)
	assert.Equal(t, "Search Tool", toolGot["name"])

	// 6. Get Tool by Slug
	reqGetSlug := newReq(t, http.MethodGet, "/v1/tools/search_tool", "", token)
	respGetSlug, err := a.App.Test(reqGetSlug)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, respGetSlug.StatusCode)

	// 7. Update Tool
	reqUpdate := newReq(t, http.MethodPut, fmt.Sprintf("/v1/tools/%s", toolID),
		`{"name":"Updated Search Tool","description":"Searches the web faster"}`, token)
	respUpdate, err := a.App.Test(reqUpdate)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, respUpdate.StatusCode)
	bodyUpdate := decodeBody(t, respUpdate)
	toolUpdated := bodyUpdate["tool"].(map[string]any)
	assert.Equal(t, "Updated Search Tool", toolUpdated["name"])
	assert.Equal(t, "Searches the web faster", toolUpdated["description"])

	// 8. Create Tool Version (draft)
	reqVer := newReq(t, http.MethodPost, fmt.Sprintf("/v1/tools/%s/versions", toolID),
		`{"input_schema":{"type":"object"},"output_schema":{"type":"string"}}`, token)
	respVer, err := a.App.Test(reqVer)
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, respVer.StatusCode)
	bodyVer := decodeBody(t, respVer)
	v1 := bodyVer["version"].(map[string]any)
	assert.Equal(t, float64(1), v1["version"])
	assert.Equal(t, "draft", v1["status"])

	// 9. Update Tool Version
	reqVerUpdate := newReq(t, http.MethodPut, fmt.Sprintf("/v1/tools/%s/versions/1", toolID),
		`{"input_schema":{"type":"object","properties":{"q":{"type":"string"}}}}`, token)
	respVerUpdate, err := a.App.Test(reqVerUpdate)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, respVerUpdate.StatusCode)

	// 10. List Tool Versions
	reqVerList := newReq(t, http.MethodGet, fmt.Sprintf("/v1/tools/%s/versions", toolID), "", token)
	respVerList, err := a.App.Test(reqVerList)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, respVerList.StatusCode)
	bodyVerList := decodeBody(t, respVerList)
	versions := bodyVerList["versions"].([]any)
	assert.Len(t, versions, 1)

	// 11. Promote Tool Version (draft -> stable)
	reqPromote := newReq(t, http.MethodPost, fmt.Sprintf("/v1/tools/%s/versions/1/promote", toolID), "", token)
	respPromote, err := a.App.Test(reqPromote)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, respPromote.StatusCode)
	bodyPromote := decodeBody(t, respPromote)
	v1Stable := bodyPromote["version"].(map[string]any)
	assert.Equal(t, "stable", v1Stable["status"])

	// Updating version when not draft should fail
	respVerUpdateFail, err := a.App.Test(reqVerUpdate)
	require.NoError(t, err)
	assert.Equal(t, http.StatusUnprocessableEntity, respVerUpdateFail.StatusCode)

	// 12. Promote Tool Version again (stable -> live)
	respPromote2, err := a.App.Test(reqPromote)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, respPromote2.StatusCode)
	bodyPromote2 := decodeBody(t, respPromote2)
	v1Live := bodyPromote2["version"].(map[string]any)
	assert.Equal(t, "live", v1Live["status"])

	// 13. Duplicate Tool Version
	reqDupVer := newReq(t, http.MethodPost, fmt.Sprintf("/v1/tools/%s/versions/1/duplicate", toolID), "", token)
	respDupVer, err := a.App.Test(reqDupVer)
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, respDupVer.StatusCode)
	bodyDupVer := decodeBody(t, respDupVer)
	v2 := bodyDupVer["version"].(map[string]any)
	assert.Equal(t, float64(2), v2["version"])
	assert.Equal(t, "draft", v2["status"])

	// 14. Demote Tool Version (live -> stable)
	reqDemote := newReq(t, http.MethodPost, fmt.Sprintf("/v1/tools/%s/versions/1/demote", toolID), "", token)
	respDemote, err := a.App.Test(reqDemote)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, respDemote.StatusCode)
	bodyDemote := decodeBody(t, respDemote)
	v1Stable2 := bodyDemote["version"].(map[string]any)
	assert.Equal(t, "stable", v1Stable2["status"])

	// 15. Archive Tool Version
	reqArchive := newReq(t, http.MethodPost, fmt.Sprintf("/v1/tools/%s/versions/1/archive", toolID), "", token)
	respArchive, err := a.App.Test(reqArchive)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, respArchive.StatusCode)
	bodyArchive := decodeBody(t, respArchive)
	v1Archived := bodyArchive["version"].(map[string]any)
	assert.Equal(t, "archived", v1Archived["status"])

	// 16. Delete Tool Version (deletes draft v2)
	reqDeleteVer := newReq(t, http.MethodDelete, fmt.Sprintf("/v1/tools/%s/versions/2", toolID), "", token)
	respDeleteVer, err := a.App.Test(reqDeleteVer)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNoContent, respDeleteVer.StatusCode)

	// Get v2 should return 404
	reqGetV2 := newReq(t, http.MethodGet, fmt.Sprintf("/v1/tools/%s/versions/2", toolID), "", token)
	respGetV2, err := a.App.Test(reqGetV2)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, respGetV2.StatusCode)

	// 17. Delete Tool
	reqDelete := newReq(t, http.MethodDelete, fmt.Sprintf("/v1/tools/%s", toolID), "", token)
	respDelete, err := a.App.Test(reqDelete)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNoContent, respDelete.StatusCode)

	// Get tool should return 404
	respGetFail, err := a.App.Test(reqGet)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, respGetFail.StatusCode)
}

func TestDiffToolVersions_Success(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)

	// Create tool
	ctx := context.Background()
	session, _ := store.GetSessionByToken(ctx, token)
	teams, _ := store.GetUserTeams(ctx, session.UserID)
	project, _ := store.CreateProject(ctx, teams[0].ID, "dt-project", "DT Project")

	req := newReq(t, http.MethodPost, "/v1/projects/"+project.ID.String()+"/tools", `{"name":"Diff Tool","url":"http://localhost"}`, token)
	resp, _ := a.Test(req)
	body := decodeBody(t, resp)
	tool := body["tool"].(map[string]any)
	toolID := tool["id"].(string)

	// V1
	reqV1 := newReq(t, http.MethodPost, "/v1/tools/"+toolID+"/versions", `{"input_schema":{"type":"object","properties":{"a":{"type":"string"}}}}`, token)
	a.Test(reqV1)

	// V2
	reqV2 := newReq(t, http.MethodPost, "/v1/tools/"+toolID+"/versions", `{"input_schema":{"type":"object","properties":{"a":{"type":"string"},"b":{"type":"number"}}}}`, token)
	a.Test(reqV2)

	// Diff V1 and V2
	reqDiff := newReq(t, http.MethodGet, "/v1/tools/"+toolID+"/versions/diff?from=1&to=2", "", token)
	respDiff, err := a.Test(reqDiff)
	require.NoError(t, err)
	AssertContract(t, respDiff)
	assert.Equal(t, http.StatusOK, respDiff.StatusCode)

	bodyDiff := decodeBody(t, respDiff)
	assert.Equal(t, float64(1), bodyDiff["from_version"])
	assert.Equal(t, float64(2), bodyDiff["to_version"])
	diffText := bodyDiff["diff"].(string)
	assert.Contains(t, diffText, `+    "b": {`)
}
