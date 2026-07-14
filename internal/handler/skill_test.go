package handler_test

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func createSkillZipBytes(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zipWriter := zip.NewWriter(&buf)
	for path, content := range files {
		f, err := zipWriter.Create(path)
		require.NoError(t, err)
		_, err = f.Write([]byte(content))
		require.NoError(t, err)
	}
	err := zipWriter.Close()
	require.NoError(t, err)
	return buf.Bytes()
}

func createMultipartReq(t *testing.T, method, url, token string, fields map[string]string, fileBytes []byte) *http.Request {
	t.Helper()
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	for k, v := range fields {
		err := writer.WriteField(k, v)
		require.NoError(t, err)
	}

	if len(fileBytes) > 0 {
		part, err := writer.CreateFormFile("file", "skill.zip")
		require.NoError(t, err)
		_, err = part.Write(fileBytes)
		require.NoError(t, err)
	}

	err := writer.Close()
	require.NoError(t, err)

	req, err := http.NewRequest(method, url, body)
	require.NoError(t, err)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	return req
}

func TestCreateSkill_Success(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	projectID := setupProject(t, a, token)

	req := newReq(t, http.MethodPost, fmt.Sprintf("/v1/projects/%s/skills", projectID),
		`{"name":"My Skill","description":"Useful skill"}`, token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)

	body := decodeBody(t, resp)
	skill := body["skill"].(map[string]any)
	assert.NotEmpty(t, skill["id"])
	assert.Equal(t, "My Skill", skill["name"])
	assert.Equal(t, "my_skill", skill["slug"])
	assert.Equal(t, projectID, skill["project_id"])
}

func TestCreateSkill_WithZip(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	projectID := setupProject(t, a, token)

	zipBytes := createSkillZipBytes(t, map[string]string{
		"index.js":     "console.log('init')",
		"package.json": "{}",
	})

	req := createMultipartReq(t, http.MethodPost, fmt.Sprintf("/v1/projects/%s/skills", projectID), token, map[string]string{
		"name":        "My Skill Zip",
		"description": "Zipped",
	}, zipBytes)

	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)

	body := decodeBody(t, resp)
	skill := body["skill"].(map[string]any)
	skillID := skill["id"].(string)

	// Fetch files in version 1
	reqFiles := newReq(t, http.MethodGet, fmt.Sprintf("/v1/skills/%s/versions/1/files", skillID), "", token)
	respFiles, err := a.Test(reqFiles)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, respFiles.StatusCode)

	bodyFiles := decodeBody(t, respFiles)
	files := bodyFiles["files"].([]any)
	assert.Len(t, files, 2)
}

func TestListAndGetSkills(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	projectID := setupProject(t, a, token)

	// Create skill
	reqCreate := newReq(t, http.MethodPost, fmt.Sprintf("/v1/projects/%s/skills", projectID),
		`{"name":"Skill One"}`, token)
	respCreate, err := a.Test(reqCreate)
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, respCreate.StatusCode)
	bodyCreate := decodeBody(t, respCreate)
	skill := bodyCreate["skill"].(map[string]any)
	skillID := skill["id"].(string)

	// List
	reqList := newReq(t, http.MethodGet, fmt.Sprintf("/v1/projects/%s/skills", projectID), "", token)
	respList, err := a.Test(reqList)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, respList.StatusCode)
	bodyList := decodeBody(t, respList)
	skills := bodyList["skills"].([]any)
	assert.Len(t, skills, 1)

	// Get by ID
	reqGetByID := newReq(t, http.MethodGet, fmt.Sprintf("/v1/skills/%s", skillID), "", token)
	respGetByID, err := a.Test(reqGetByID)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, respGetByID.StatusCode)

	// Get by slug
	reqGetBySlug := newReq(t, http.MethodGet, fmt.Sprintf("/v1/skills/skill_one"), "", token)
	respGetBySlug, err := a.Test(reqGetBySlug)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, respGetBySlug.StatusCode)
}

func TestUpdateAndDeleteSkill(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	projectID := setupProject(t, a, token)

	// Create skill
	reqCreate := newReq(t, http.MethodPost, fmt.Sprintf("/v1/projects/%s/skills", projectID),
		`{"name":"Old Skill"}`, token)
	respCreate, err := a.Test(reqCreate)
	require.NoError(t, err)
	bodyCreate := decodeBody(t, respCreate)
	skill := bodyCreate["skill"].(map[string]any)
	skillID := skill["id"].(string)

	// Update skill
	reqUpdate := newReq(t, http.MethodPut, fmt.Sprintf("/v1/skills/%s", skillID),
		`{"name":"New Skill","description":"Updated"}`, token)
	respUpdate, err := a.Test(reqUpdate)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, respUpdate.StatusCode)
	bodyUpdate := decodeBody(t, respUpdate)
	assert.Equal(t, "New Skill", bodyUpdate["skill"].(map[string]any)["name"])

	// Delete skill
	reqDelete := newReq(t, http.MethodDelete, fmt.Sprintf("/v1/skills/%s", skillID), "", token)
	respDelete, err := a.Test(reqDelete)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, respDelete.StatusCode)
}

func TestSkillVersions_PromoteDemoteDuplicate(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	projectID := setupProject(t, a, token)

	// Create skill
	reqCreate := newReq(t, http.MethodPost, fmt.Sprintf("/v1/projects/%s/skills", projectID),
		`{"name":"Skill Version Test"}`, token)
	respCreate, err := a.Test(reqCreate)
	require.NoError(t, err)
	bodyCreate := decodeBody(t, respCreate)
	skill := bodyCreate["skill"].(map[string]any)
	skillID := skill["id"].(string)

	// List versions (v1 default)
	reqList := newReq(t, http.MethodGet, fmt.Sprintf("/v1/skills/%s/versions", skillID), "", token)
	respList, err := a.Test(reqList)
	require.NoError(t, err)
	bodyList := decodeBody(t, respList)
	versions := bodyList["versions"].([]any)
	assert.Len(t, versions, 1)

	// Duplicate version 1
	reqDup := newReq(t, http.MethodPost, fmt.Sprintf("/v1/skills/%s/versions/1/duplicate", skillID), "", token)
	respDup, err := a.Test(reqDup)
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, respDup.StatusCode)
	bodyDup := decodeBody(t, respDup)
	assert.Equal(t, float64(2), bodyDup["version"].(map[string]any)["version"])

	// Promote version 1 (draft -> stable)
	reqProm := newReq(t, http.MethodPost, fmt.Sprintf("/v1/skills/%s/versions/1/promote", skillID), "", token)
	respProm, err := a.Test(reqProm)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, respProm.StatusCode)
	bodyProm := decodeBody(t, respProm)
	assert.Equal(t, "stable", bodyProm["version"].(map[string]any)["status"])

	// Promote version 1 again (stable -> live)
	reqPromLive := newReq(t, http.MethodPost, fmt.Sprintf("/v1/skills/%s/versions/1/promote", skillID), "", token)
	respPromLive, err := a.Test(reqPromLive)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, respPromLive.StatusCode)

	// Demote version 1 (live -> stable)
	reqDemote := newReq(t, http.MethodPost, fmt.Sprintf("/v1/skills/%s/versions/1/demote", skillID), "", token)
	respDemote, err := a.Test(reqDemote)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, respDemote.StatusCode)

	// Archive version 1
	reqArchive := newReq(t, http.MethodPost, fmt.Sprintf("/v1/skills/%s/versions/1/archive", skillID), "", token)
	respArchive, err := a.Test(reqArchive)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, respArchive.StatusCode)
}

func TestSkillFiles_IndividualOperations(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	projectID := setupProject(t, a, token)

	// Create skill
	reqCreate := newReq(t, http.MethodPost, fmt.Sprintf("/v1/projects/%s/skills", projectID),
		`{"name":"File Test Skill"}`, token)
	respCreate, err := a.Test(reqCreate)
	require.NoError(t, err)
	bodyCreate := decodeBody(t, respCreate)
	skill := bodyCreate["skill"].(map[string]any)
	skillID := skill["id"].(string)

	// Save individual file (draft)
	reqFileSave := newReq(t, http.MethodPost, fmt.Sprintf("/v1/skills/%s/versions/1/files", skillID),
		`{"file_path":"test.txt","content":"hello world"}`, token)
	respFileSave, err := a.Test(reqFileSave)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, respFileSave.StatusCode)

	// Get file content
	reqFileContent := newReq(t, http.MethodGet, fmt.Sprintf("/v1/skills/%s/versions/1/files/content?file_path=test.txt", skillID), "", token)
	respFileContent, err := a.Test(reqFileContent)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, respFileContent.StatusCode)
	bodyContent := decodeBody(t, respFileContent)
	assert.Equal(t, "hello world", bodyContent["content"])

	// Update file content (PUT)
	reqFileUpdate := newReq(t, http.MethodPut, fmt.Sprintf("/v1/skills/%s/versions/1/files", skillID),
		`{"file_path":"test.txt","content":"hello updated"}`, token)
	respFileUpdate, err := a.Test(reqFileUpdate)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, respFileUpdate.StatusCode)

	// Recheck content
	reqFileContent2 := newReq(t, http.MethodGet, fmt.Sprintf("/v1/skills/%s/versions/1/files/content?file_path=test.txt", skillID), "", token)
	respFileContent2, err := a.Test(reqFileContent2)
	require.NoError(t, err)
	bodyContent2 := decodeBody(t, respFileContent2)
	assert.Equal(t, "hello updated", bodyContent2["content"])

	// Delete individual file
	reqFileDelete := newReq(t, http.MethodDelete, fmt.Sprintf("/v1/skills/%s/versions/1/files?file_path=test.txt", skillID), "", token)
	respFileDelete, err := a.Test(reqFileDelete)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, respFileDelete.StatusCode)
}

func TestSkillZip_UploadAndDownload(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	projectID := setupProject(t, a, token)

	// Create skill
	reqCreate := newReq(t, http.MethodPost, fmt.Sprintf("/v1/projects/%s/skills", projectID),
		`{"name":"Zip Test Skill"}`, token)
	respCreate, err := a.Test(reqCreate)
	require.NoError(t, err)
	bodyCreate := decodeBody(t, respCreate)
	skill := bodyCreate["skill"].(map[string]any)
	skillID := skill["id"].(string)

	zipBytes := createSkillZipBytes(t, map[string]string{
		"main.js":    "console.log('running')",
		"config.yml": "port: 8080",
	})

	// Upload zip to create a new draft version (version 2)
	reqUpload := createMultipartReq(t, http.MethodPut, fmt.Sprintf("/v1/skills/%s/versions", skillID), token, nil, zipBytes)
	respUpload, err := a.Test(reqUpload)
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, respUpload.StatusCode)

	bodyUpload := decodeBody(t, respUpload)
	versionObj := bodyUpload["version"].(map[string]any)
	versionNum := int(versionObj["version"].(float64))
	assert.Equal(t, 2, versionNum)

	// Download zip
	reqDownload := newReq(t, http.MethodGet, fmt.Sprintf("/v1/skills/%s/versions/%d/download", skillID, versionNum), "", token)
	respDownload, err := a.Test(reqDownload)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, respDownload.StatusCode)
	assert.Equal(t, "application/zip", respDownload.Header.Get("Content-Type"))

	// Verify download bytes are a valid zip with correct files
	downloadedBytes, err := io.ReadAll(respDownload.Body)
	require.NoError(t, err)
	respDownload.Body.Close()

	reader, err := zip.NewReader(bytes.NewReader(downloadedBytes), int64(len(downloadedBytes)))
	require.NoError(t, err)
	assert.Len(t, reader.File, 2)

	filesFound := map[string]string{}
	for _, f := range reader.File {
		rc, err := f.Open()
		require.NoError(t, err)
		fc, err := io.ReadAll(rc)
		require.NoError(t, err)
		rc.Close()
		filesFound[f.Name] = string(fc)
	}

	assert.Equal(t, "console.log('running')", filesFound["main.js"])
	assert.Equal(t, "port: 8080", filesFound["config.yml"])
}

func TestSkillZip_TemplatizedDownloadAndGetFileContent(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	projectID := setupProject(t, a, token)

	// Create skill
	reqCreate := newReq(t, http.MethodPost, fmt.Sprintf("/v1/projects/%s/skills", projectID),
		`{"name":"Template Skill"}`, token)
	respCreate, err := a.Test(reqCreate)
	require.NoError(t, err)
	bodyCreate := decodeBody(t, respCreate)
	skill := bodyCreate["skill"].(map[string]any)
	skillID := skill["id"].(string)

	// We upload a ZIP with template files
	zipBytes := createSkillZipBytes(t, map[string]string{
		"config.json": `{"env": "{{.Env}}", "debug": {{.Debug}}}`,
		"binary.bin":  string([]byte{0x00, 0x01, 0x02, 0x03}), // Should not be rendered (binary)
	})

	// Upload zip to create version 2
	reqUpload := createMultipartReq(t, http.MethodPut, fmt.Sprintf("/v1/skills/%s/versions", skillID), token, nil, zipBytes)
	respUpload, err := a.Test(reqUpload)
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, respUpload.StatusCode)

	bodyUpload := decodeBody(t, respUpload)
	versionObj := bodyUpload["version"].(map[string]any)
	versionNum := int(versionObj["version"].(float64))
	assert.Equal(t, 2, versionNum)

	// 1. Test GetSkillFileContent (Individual File) with variables via query parameters
	reqGetFile := newReq(t, http.MethodGet, fmt.Sprintf("/v1/skills/%s/versions/%d/files/content?file_path=config.json&Env=Production&Debug=true", skillID, versionNum), "", token)
	respGetFile, err := a.Test(reqGetFile)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, respGetFile.StatusCode)
	bodyGetFile := decodeBody(t, respGetFile)
	assert.Equal(t, `{"env": "Production", "debug": true}`, bodyGetFile["content"].(string))

	// 2. Test GetSkillFileContent (Individual File) - Failure Path (Missing Variable with missingkey=error)
	reqGetFileFail := newReq(t, http.MethodGet, fmt.Sprintf("/v1/skills/%s/versions/%d/files/content?file_path=config.json&Env=Production", skillID, versionNum), "", token)
	respGetFileFail, err := a.Test(reqGetFileFail)
	require.NoError(t, err)
	assert.Equal(t, http.StatusUnprocessableEntity, respGetFileFail.StatusCode)

	// 3. Test DownloadSkillZip (Zipped Archive) with variables via query parameters
	reqDownload := newReq(t, http.MethodGet, fmt.Sprintf("/v1/skills/%s/versions/%d/download?Env=Staging&Debug=false", skillID, versionNum), "", token)
	respDownload, err := a.Test(reqDownload)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, respDownload.StatusCode)

	downloadedBytes, err := io.ReadAll(respDownload.Body)
	require.NoError(t, err)
	respDownload.Body.Close()

	reader, err := zip.NewReader(bytes.NewReader(downloadedBytes), int64(len(downloadedBytes)))
	require.NoError(t, err)
	assert.Len(t, reader.File, 2)

	filesFound := map[string]string{}
	for _, f := range reader.File {
		rc, err := f.Open()
		require.NoError(t, err)
		fc, err := io.ReadAll(rc)
		require.NoError(t, err)
		rc.Close()
		filesFound[f.Name] = string(fc)
	}

	assert.Equal(t, `{"env": "Staging", "debug": false}`, filesFound["config.json"])
	assert.Equal(t, string([]byte{0x00, 0x01, 0x02, 0x03}), filesFound["binary.bin"])
}
