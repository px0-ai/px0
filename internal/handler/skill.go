package handler

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"github.com/px0-ai/px0/internal/apierr"
	"github.com/px0-ai/px0/internal/model"
	"github.com/px0-ai/px0/internal/store"
)

// Helper to resolve skill by ID or slug and check viewer access.
func resolveSkill(c *fiber.Ctx) (*model.Skill, error) {
	param := c.Params("id")
	projectIDs, err := getRequestViewerProjectIDs(c)
	if err != nil {
		return nil, fmt.Errorf("viewer projects: %w", err)
	}

	var skill *model.Skill
	id, err := uuid.Parse(param)
	if err == nil {
		skill, err = store.GetSkillByID(c.Context(), id, projectIDs)
	} else {
		skill, err = store.GetSkillBySlug(c.Context(), param, projectIDs)
	}

	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, store.ErrNotFound
		}
		return nil, err
	}
	return skill, nil
}

// Helper to check if user has editor permission on skill's project.
func checkSkillEditor(c *fiber.Ctx, skill *model.Skill) (bool, error) {
	editorProjectIDs, err := getRequestEditorProjectIDs(c)
	if err != nil {
		return false, err
	}
	for _, pid := range editorProjectIDs {
		if pid == skill.ProjectID {
			return true, nil
		}
	}
	return false, nil
}

// Helper to parse version number from param.
func parseVersionNum(c *fiber.Ctx) (int, error) {
	vStr := c.Params("version")
	vNum, err := strconv.Atoi(vStr)
	if err != nil || vNum <= 0 {
		return 0, errors.New("invalid version number")
	}
	return vNum, nil
}

// Helper to unzip uploaded bytes.
func unzipBytes(zipBytes []byte, skillID uuid.UUID, versionID uuid.UUID) ([]model.SkillFile, error) {
	reader, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		return nil, err
	}
	var files []model.SkillFile
	for _, file := range reader.File {
		if file.FileInfo().IsDir() {
			continue
		}
		rc, err := file.Open()
		if err != nil {
			return nil, err
		}
		var buf bytes.Buffer
		_, err = io.Copy(&buf, rc)
		rc.Close()
		if err != nil {
			return nil, err
		}
		files = append(files, model.SkillFile{
			SkillID:   skillID,
			VersionID: versionID,
			FilePath:  file.Name,
			Content:   buf.Bytes(),
		})
	}
	return files, nil
}

type createSkillRequest struct {
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Description string `json:"description"`
}

// CreateSkill creates a skill under a project (requires editor role).
func CreateSkill(c *fiber.Ctx) error {
	projectID, err := uuid.Parse(c.Params("projectID"))
	if err != nil {
		return apierr.ErrInvalidID.Respond(c)
	}

	editorIDs, err := getRequestEditorProjectIDs(c)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	if !authorizeProject(c, projectID, editorIDs) {
		return nil
	}

	var name, slugStr, description string
	var fileBytes []byte

	contentType := c.Get("Content-Type")
	if strings.Contains(contentType, "multipart/form-data") {
		name = c.FormValue("name")
		slugStr = c.FormValue("slug")
		description = c.FormValue("description")

		fileHeader, err := c.FormFile("file")
		if err == nil && fileHeader != nil {
			f, err := fileHeader.Open()
			if err != nil {
				return apierr.ErrInternalError.Respond(c, err)
			}
			defer f.Close()
			var buf bytes.Buffer
			if _, err := io.Copy(&buf, f); err != nil {
				return apierr.ErrInternalError.Respond(c, err)
			}
			fileBytes = buf.Bytes()
		}
	} else {
		var req createSkillRequest
		if err := c.BodyParser(&req); err != nil {
			return apierr.ErrInvalidRequestBody.Respond(c)
		}
		name = req.Name
		slugStr = req.Slug
		description = req.Description
	}

	if name == "" {
		return apierr.ErrNameRequired.Respond(c)
	}

	if slugStr == "" {
		slugStr = name
	}
	slugStr = NormalizeSlug(slugStr)

	skill, err := store.CreateSkill(c.Context(), projectID, slugStr, name, description)
	if err != nil {
		if errors.Is(err, store.ErrDuplicate) {
			return apierr.NewAPIError(fiber.StatusConflict, "skill with this name or slug already exists in project").Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	// If zip file was uploaded, unzip and insert files into version 1.
	if len(fileBytes) > 0 {
		v, err := store.GetSkillVersion(c.Context(), skill.ID, 1)
		if err != nil {
			return apierr.ErrInternalError.Respond(c, err)
		}
		files, err := unzipBytes(fileBytes, skill.ID, v.ID)
		if err != nil {
			return apierr.NewAPIError(fiber.StatusBadRequest, "invalid zip file: "+err.Error()).Respond(c)
		}
		if err := store.ReplaceSkillFiles(c.Context(), v.ID, skill.ID, files); err != nil {
			return apierr.ErrInternalError.Respond(c, err)
		}
	}

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"skill": skill})
}

// ListSkills lists all skills in a project (requires viewer role).
func ListSkills(c *fiber.Ctx) error {
	projectID, err := uuid.Parse(c.Params("projectID"))
	if err != nil {
		return apierr.ErrInvalidID.Respond(c)
	}

	viewerIDs, err := getRequestViewerProjectIDs(c)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	if !authorizeProject(c, projectID, viewerIDs) {
		return nil
	}

	skills, err := store.ListSkills(c.Context(), []uuid.UUID{projectID})
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	return c.JSON(fiber.Map{"skills": skills})
}

// GetSkill returns details of a specific skill.
func GetSkill(c *fiber.Ctx) error {
	skill, err := resolveSkill(c)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrSkillNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}
	return c.JSON(fiber.Map{"skill": skill})
}

type updateSkillRequest struct {
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Description string `json:"description"`
}

// UpdateSkill updates skill details (requires editor role).
func UpdateSkill(c *fiber.Ctx) error {
	skill, err := resolveSkill(c)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrSkillNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	isEditor, err := checkSkillEditor(c, skill)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	if !isEditor {
		return apierr.ErrForbidden.Respond(c)
	}

	var req updateSkillRequest
	if err := c.BodyParser(&req); err != nil {
		return apierr.ErrInvalidRequestBody.Respond(c)
	}

	name := req.Name
	if name == "" {
		name = skill.Name
	}
	slugStr := req.Slug
	if slugStr == "" {
		slugStr = skill.Slug
	}
	slugStr = NormalizeSlug(slugStr)

	editorProjectIDs, err := getRequestEditorProjectIDs(c)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	updated, err := store.UpdateSkill(c.Context(), skill.ID, editorProjectIDs, slugStr, name, req.Description)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrSkillNotFound.Respond(c)
		}
		if errors.Is(err, store.ErrDuplicate) {
			return apierr.NewAPIError(fiber.StatusConflict, "skill with this name or slug already exists in project").Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	return c.JSON(fiber.Map{"skill": updated})
}

// DeleteSkill deletes a skill (requires editor role).
func DeleteSkill(c *fiber.Ctx) error {
	skill, err := resolveSkill(c)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrSkillNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	isEditor, err := checkSkillEditor(c, skill)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	if !isEditor {
		return apierr.ErrForbidden.Respond(c)
	}

	editorProjectIDs, err := getRequestEditorProjectIDs(c)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	err = store.DeleteSkill(c.Context(), skill.ID, editorProjectIDs)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrSkillNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	return c.JSON(fiber.Map{"message": "skill deleted successfully"})
}

// CreateSkillVersion creates a new empty draft version.
func CreateSkillVersion(c *fiber.Ctx) error {
	skill, err := resolveSkill(c)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrSkillNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	isEditor, err := checkSkillEditor(c, skill)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	if !isEditor {
		return apierr.ErrForbidden.Respond(c)
	}

	v, err := store.CreateSkillVersion(c.Context(), skill.ID)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"version": v})
}

// DuplicateSkillVersion duplicates an existing version.
func DuplicateSkillVersion(c *fiber.Ctx) error {
	skill, err := resolveSkill(c)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrSkillNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	isEditor, err := checkSkillEditor(c, skill)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	if !isEditor {
		return apierr.ErrForbidden.Respond(c)
	}

	sourceVer, err := parseVersionNum(c)
	if err != nil {
		return apierr.ErrInvalidVersionNumber.Respond(c)
	}

	v, err := store.DuplicateSkillVersion(c.Context(), skill.ID, sourceVer)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrVersionNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"version": v})
}

// ListSkillVersions lists all versions.
func ListSkillVersions(c *fiber.Ctx) error {
	skill, err := resolveSkill(c)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrSkillNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	versions, err := store.ListSkillVersions(c.Context(), skill.ID)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	return c.JSON(fiber.Map{"versions": versions})
}

// GetSkillVersion returns details of a version.
func GetSkillVersion(c *fiber.Ctx) error {
	skill, err := resolveSkill(c)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrSkillNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	ver, err := parseVersionNum(c)
	if err != nil {
		return apierr.ErrInvalidVersionNumber.Respond(c)
	}

	v, err := store.GetSkillVersion(c.Context(), skill.ID, ver)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrVersionNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	return c.JSON(fiber.Map{"version": v})
}

// DeleteSkillVersion deletes a draft version.
func DeleteSkillVersion(c *fiber.Ctx) error {
	skill, err := resolveSkill(c)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrSkillNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	isEditor, err := checkSkillEditor(c, skill)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	if !isEditor {
		return apierr.ErrForbidden.Respond(c)
	}

	ver, err := parseVersionNum(c)
	if err != nil {
		return apierr.ErrInvalidVersionNumber.Respond(c)
	}

	err = store.DeleteSkillVersion(c.Context(), skill.ID, ver)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrVersionNotFound.Respond(c)
		}
		if errors.Is(err, store.ErrConflict) {
			return apierr.ErrOnlyDraftsDeletable.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	return c.JSON(fiber.Map{"message": "version deleted successfully"})
}

// PromoteSkillVersion promotes a version status (draft -> stable -> live).
func PromoteSkillVersion(c *fiber.Ctx) error {
	skill, err := resolveSkill(c)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrSkillNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	isEditor, err := checkSkillEditor(c, skill)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	if !isEditor {
		return apierr.ErrForbidden.Respond(c)
	}

	ver, err := parseVersionNum(c)
	if err != nil {
		return apierr.ErrInvalidVersionNumber.Respond(c)
	}

	v, err := store.PromoteSkillVersion(c.Context(), skill.ID, ver)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrVersionNotFound.Respond(c)
		}
		if errors.Is(err, store.ErrConflict) {
			return apierr.NewAPIError(fiber.StatusConflict, err.Error()).Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	return c.JSON(fiber.Map{"version": v})
}

// DemoteSkillVersion demotes a live version to stable.
func DemoteSkillVersion(c *fiber.Ctx) error {
	skill, err := resolveSkill(c)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrSkillNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	isEditor, err := checkSkillEditor(c, skill)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	if !isEditor {
		return apierr.ErrForbidden.Respond(c)
	}

	ver, err := parseVersionNum(c)
	if err != nil {
		return apierr.ErrInvalidVersionNumber.Respond(c)
	}

	v, err := store.DemoteSkillVersion(c.Context(), skill.ID, ver)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrVersionNotFound.Respond(c)
		}
		if errors.Is(err, store.ErrConflict) {
			return apierr.NewAPIError(fiber.StatusConflict, err.Error()).Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	return c.JSON(fiber.Map{"version": v})
}

// ArchiveSkillVersion archives a version.
func ArchiveSkillVersion(c *fiber.Ctx) error {
	skill, err := resolveSkill(c)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrSkillNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	isEditor, err := checkSkillEditor(c, skill)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	if !isEditor {
		return apierr.ErrForbidden.Respond(c)
	}

	ver, err := parseVersionNum(c)
	if err != nil {
		return apierr.ErrInvalidVersionNumber.Respond(c)
	}

	v, err := store.ArchiveSkillVersion(c.Context(), skill.ID, ver)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrVersionNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	return c.JSON(fiber.Map{"version": v})
}

// UploadSkillZip handles uploading a zip to a draft version.
func UploadSkillZip(c *fiber.Ctx) error {
	skill, err := resolveSkill(c)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrSkillNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	isEditor, err := checkSkillEditor(c, skill)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	if !isEditor {
		return apierr.ErrForbidden.Respond(c)
	}

	ver, err := parseVersionNum(c)
	if err != nil {
		return apierr.ErrInvalidVersionNumber.Respond(c)
	}

	v, err := store.GetSkillVersion(c.Context(), skill.ID, ver)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrVersionNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	if v.Status != "draft" {
		return apierr.ErrOnlyDraftsModifiable.Respond(c)
	}

	fileHeader, err := c.FormFile("file")
	if err != nil {
		return apierr.NewAPIError(fiber.StatusBadRequest, "file is required").Respond(c)
	}

	f, err := fileHeader.Open()
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	defer f.Close()

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, f); err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	files, err := unzipBytes(buf.Bytes(), skill.ID, v.ID)
	if err != nil {
		return apierr.NewAPIError(fiber.StatusBadRequest, "invalid zip file: "+err.Error()).Respond(c)
	}

	if err := store.ReplaceSkillFiles(c.Context(), v.ID, skill.ID, files); err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	return c.JSON(fiber.Map{"message": "files uploaded and replaced successfully"})
}

// DownloadSkillZip packs all files in a version into a zip and sends it.
func DownloadSkillZip(c *fiber.Ctx) error {
	skill, err := resolveSkill(c)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrSkillNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	ver, err := parseVersionNum(c)
	if err != nil {
		return apierr.ErrInvalidVersionNumber.Respond(c)
	}

	v, err := store.GetSkillVersion(c.Context(), skill.ID, ver)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrVersionNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	files, err := store.GetSkillFiles(c.Context(), v.ID)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	var buf bytes.Buffer
	zipWriter := zip.NewWriter(&buf)

	for _, file := range files {
		f, err := zipWriter.Create(file.FilePath)
		if err != nil {
			return apierr.ErrInternalError.Respond(c, err)
		}
		_, err = f.Write(file.Content)
		if err != nil {
			return apierr.ErrInternalError.Respond(c, err)
		}
	}

	err = zipWriter.Close()
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	c.Set("Content-Type", "application/zip")
	c.Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s-v%d.zip", skill.Slug, ver))
	return c.Send(buf.Bytes())
}

// ListSkillFiles lists meta info about files in a version (omits raw content in JSON).
func ListSkillFiles(c *fiber.Ctx) error {
	skill, err := resolveSkill(c)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrSkillNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	ver, err := parseVersionNum(c)
	if err != nil {
		return apierr.ErrInvalidVersionNumber.Respond(c)
	}

	v, err := store.GetSkillVersion(c.Context(), skill.ID, ver)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrVersionNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	files, err := store.GetSkillFiles(c.Context(), v.ID)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	if files == nil {
		files = []model.SkillFile{}
	}

	return c.JSON(fiber.Map{"files": files})
}

// GetSkillFileContent returns content of an individual file.
func GetSkillFileContent(c *fiber.Ctx) error {
	skill, err := resolveSkill(c)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrSkillNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	ver, err := parseVersionNum(c)
	if err != nil {
		return apierr.ErrInvalidVersionNumber.Respond(c)
	}

	filePath := c.Query("file_path")
	if filePath == "" {
		return apierr.ErrFilePathRequired.Respond(c)
	}

	v, err := store.GetSkillVersion(c.Context(), skill.ID, ver)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrVersionNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	file, err := store.GetSkillFile(c.Context(), v.ID, filePath)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrFileNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	return c.JSON(fiber.Map{
		"file_path":  file.FilePath,
		"content":    string(file.Content),
		"created_at": file.CreatedAt,
		"updated_at": file.UpdatedAt,
	})
}

type fileUpsertRequest struct {
	FilePath string `json:"file_path"`
	Content  string `json:"content"`
}

// UpsertSkillFile creates or updates an individual file.
func UpsertSkillFile(c *fiber.Ctx) error {
	skill, err := resolveSkill(c)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrSkillNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	isEditor, err := checkSkillEditor(c, skill)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	if !isEditor {
		return apierr.ErrForbidden.Respond(c)
	}

	ver, err := parseVersionNum(c)
	if err != nil {
		return apierr.ErrInvalidVersionNumber.Respond(c)
	}

	v, err := store.GetSkillVersion(c.Context(), skill.ID, ver)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrVersionNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	if v.Status != "draft" {
		return apierr.ErrOnlyDraftsModifiable.Respond(c)
	}

	var req fileUpsertRequest
	if err := c.BodyParser(&req); err != nil {
		return apierr.ErrInvalidRequestBody.Respond(c)
	}

	if req.FilePath == "" {
		return apierr.ErrFilePathRequired.Respond(c)
	}

	err = store.UpsertSkillFile(c.Context(), v.ID, skill.ID, req.FilePath, []byte(req.Content))
	if err != nil {
		if errors.Is(err, store.ErrConflict) {
			return apierr.ErrOnlyDraftsModifiable.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	return c.JSON(fiber.Map{"message": "file saved successfully"})
}

// DeleteSkillFile deletes an individual file.
func DeleteSkillFile(c *fiber.Ctx) error {
	skill, err := resolveSkill(c)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrSkillNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	isEditor, err := checkSkillEditor(c, skill)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	if !isEditor {
		return apierr.ErrForbidden.Respond(c)
	}

	ver, err := parseVersionNum(c)
	if err != nil {
		return apierr.ErrInvalidVersionNumber.Respond(c)
	}

	filePath := c.Query("file_path")
	if filePath == "" {
		return apierr.ErrFilePathRequired.Respond(c)
	}

	v, err := store.GetSkillVersion(c.Context(), skill.ID, ver)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrVersionNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	if v.Status != "draft" {
		return apierr.ErrOnlyDraftsModifiable.Respond(c)
	}

	err = store.DeleteSkillFile(c.Context(), v.ID, filePath)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrFileNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	return c.JSON(fiber.Map{"message": "file deleted successfully"})
}
