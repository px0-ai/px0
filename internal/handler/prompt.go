package handler

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/pmezard/go-difflib/difflib"

	"github.com/px0-ai/px0/internal/apierr"
	"github.com/px0-ai/px0/internal/middleware"
	"github.com/px0-ai/px0/internal/model"
	"github.com/px0-ai/px0/internal/search"
	"github.com/px0-ai/px0/internal/store"
)

type createPromptRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Slug        string `json:"slug"`
}

// parseVectorParam parses a comma-separated float32 slice from a query string value.
// Returns nil if the string is empty, or an error if any token is not a valid float.
func parseVectorParam(raw string) ([]float32, error) {
	if raw == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	vec := make([]float32, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		f, err := strconv.ParseFloat(p, 32)
		if err != nil {
			return nil, fmt.Errorf("invalid vector value %q: %w", p, err)
		}
		vec = append(vec, float32(f))
	}
	return vec, nil
}

func NormalizeSlug(s string) string {
	s = strings.ToLower(s)
	var sb strings.Builder
	lastUnderscore := false
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			sb.WriteRune(r)
			lastUnderscore = false
		} else if r == '_' {
			if !lastUnderscore {
				sb.WriteRune('_')
				lastUnderscore = true
			}
		} else {
			if sb.Len() > 0 && !lastUnderscore {
				sb.WriteRune('_')
				lastUnderscore = true
			}
		}
	}
	res := sb.String()
	res = strings.Trim(res, "_")
	return res
}

func CreatePrompt(c *fiber.Ctx) error {
	teamID, err := uuid.Parse(c.Params("teamID"))
	if err != nil {
		return apierr.ErrInvalidID.Respond(c)
	}

	allowedIDs, err := getRequestEditorTeamIDs(c)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	isAllowed := false
	for _, id := range allowedIDs {
		if id == teamID {
			isAllowed = true
			break
		}
	}
	if !isAllowed {
		return apierr.ErrForbidden.Respond(c)
	}

	var req createPromptRequest
	if err := c.BodyParser(&req); err != nil {
		return apierr.ErrInvalidRequestBody.Respond(c)
	}
	if req.Name == "" {
		return apierr.ErrNameRequired.Respond(c)
	}

	// Normalize slug if provided; otherwise, generate from name and normalize.
	slug := req.Slug
	if slug == "" {
		slug = req.Name
	}
	slug = NormalizeSlug(slug)

	prompt, err := store.CreatePrompt(c.Context(), teamID, slug, req.Name, req.Description)
	if err != nil {
		if errors.Is(err, store.ErrDuplicate) {
			return apierr.NewAPIError(fiber.StatusConflict, "prompt with this name or slug already exists; please provide a unique name").Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	syncPromptSearchIndex(c.Context(), prompt)

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"prompt": prompt})
}

func ListPrompts(c *fiber.Ctx) error {
	teamID, err := uuid.Parse(c.Params("teamID"))
	if err != nil {
		return apierr.ErrInvalidID.Respond(c)
	}

	allowedIDs, err := getRequestTeamIDs(c)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	isAllowed := false
	for _, id := range allowedIDs {
		if id == teamID {
			isAllowed = true
			break
		}
	}
	if !isAllowed {
		return apierr.ErrForbidden.Respond(c)
	}

	status, archived := parsePromptStatusFilter(c)

	// Full-text search and vector search path.
	// Vector takes precedence if provided.
	vectorRaw := c.Query("vector")
	vector, err := parseVectorParam(vectorRaw)
	if err != nil {
		return apierr.NewAPIError(fiber.StatusBadRequest, "invalid vector parameter: "+err.Error()).Respond(c)
	}

	topK := 10
	if k := c.QueryInt("top_k", 10); k > 0 {
		topK = k
	}

	if vector != nil {
		results, err := search.Get().Search(c.Context(), search.SearchQuery{
			Vector:  vector,
			TopK:    topK,
			TeamIDs: []uuid.UUID{teamID},
			Status:  status,
		})
		if err != nil {
			if errors.Is(err, search.ErrVectorSearchNotSupported) {
				return apierr.NewAPIError(fiber.StatusBadRequest, "vector search not supported by the active search provider").Respond(c)
			}
			if errors.Is(err, search.ErrNotImplemented) {
				// Provider is a NoopProvider; fall through to the store layer below.
			} else {
				return apierr.ErrInternalError.Respond(c, err)
			}
		} else {
			if len(results) == 0 {
				return c.JSON(fiber.Map{"prompts": []*model.Prompt{}})
			}
			ids := make([]uuid.UUID, len(results))
			for i, r := range results {
				ids[i] = r.PromptID
			}
			prompts, err := store.GetPromptsByIDs(c.Context(), ids, []uuid.UUID{teamID}, status)
			if err != nil {
				return apierr.ErrInternalError.Respond(c, err)
			}
			return c.JSON(fiber.Map{"prompts": prompts})
		}
	} else if q := c.Query("q"); q != "" {
		results, err := search.Get().Search(c.Context(), search.SearchQuery{
			Q:       q,
			TeamIDs: []uuid.UUID{teamID},
			Status:  status,
		})
		if err != nil {
			if errors.Is(err, search.ErrNotImplemented) {
				// Provider is a NoopProvider; fall through to the store layer below.
			} else {
				return apierr.ErrInternalError.Respond(c, err)
			}
		} else {
			if len(results) == 0 {
				return c.JSON(fiber.Map{"prompts": []*model.Prompt{}})
			}
			ids := make([]uuid.UUID, len(results))
			for i, r := range results {
				ids[i] = r.PromptID
			}
			prompts, err := store.GetPromptsByIDs(c.Context(), ids, []uuid.UUID{teamID}, status)
			if err != nil {
				return apierr.ErrInternalError.Respond(c, err)
			}
			return c.JSON(fiber.Map{"prompts": prompts})
		}
	}

	prompts, err := store.ListPrompts(c.Context(), store.PromptFilter{
		TeamIDs:  []uuid.UUID{teamID},
		Archived: archived,
		Status:   status,
		Q:        c.Query("q"),
	})
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	if prompts == nil {
		prompts = []*model.Prompt{}
	}
	return c.JSON(fiber.Map{"prompts": prompts})
}

func GetPrompt(c *fiber.Ctx) error {
	param := c.Params("id")
	var prompt *model.Prompt
	var err error

	teamIDs, err := getRequestTeamIDs(c)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	id, err := uuid.Parse(param)
	if err == nil {
		prompt, err = store.GetPromptByID(c.Context(), id, teamIDs)
	} else {
		prompt, err = store.GetPromptBySlug(c.Context(), param, teamIDs)
	}

	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrPromptNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	// Now check for optional version or tag query parameter
	versionParam := c.Query("version")
	tagParam := c.Query("tag")

	var version *model.PromptVersion
	if versionParam != "" {
		version, err = resolveVersion(c.Context(), prompt.ID, versionParam)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return apierr.ErrVersionNotFound.Respond(c)
			}
			return apierr.ErrInternalError.Respond(c, err)
		}
	} else if tagParam != "" {
		version, err = store.GetVersionByTag(c.Context(), prompt.ID, tagParam)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return apierr.ErrVersionNotFound.Respond(c)
			}
			return apierr.ErrInternalError.Respond(c, err)
		}
	}

	resp := fiber.Map{"prompt": prompt}
	if version != nil {
		resp["version"] = version
	}

	return c.JSON(resp)
}

func ArchivePrompt(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apierr.ErrInvalidPromptID.Respond(c)
	}

	teamIDs, err := getRequestTeamIDs(c)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	if _, err := store.GetPromptByID(c.Context(), id, teamIDs); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrPromptNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	adminTeamIDs, err := getRequestAdminTeamIDs(c)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	if err := store.ArchivePrompt(c.Context(), id, adminTeamIDs); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrForbidden.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	_ = search.Get().Deindex(c.Context(), id)

	prompt, err := store.GetPromptByID(c.Context(), id, teamIDs)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	return c.JSON(fiber.Map{"prompt": prompt})
}

func ListAllPrompts(c *fiber.Ctx) error {
	teamIDStr := c.Query("team")
	if teamIDStr == "" {
		teamIDStr = c.Query("team_id")
	}

	if teamIDStr == "" {
		// By default nothing is shown as per backwards-compatible test requirements
		return c.JSON(fiber.Map{"prompts": []*model.Prompt{}})
	}

	teamID, err := uuid.Parse(teamIDStr)
	if err != nil {
		return apierr.ErrInvalidID.Respond(c)
	}

	allowedIDs, err := getRequestTeamIDs(c)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	isAllowed := false
	for _, id := range allowedIDs {
		if id == teamID {
			isAllowed = true
			break
		}
	}
	if !isAllowed {
		return apierr.ErrForbidden.Respond(c)
	}

	status, archived := parsePromptStatusFilter(c)

	// Full-text search and vector search path.
	// Vector takes precedence if provided.
	vectorRaw := c.Query("vector")
	vector, err := parseVectorParam(vectorRaw)
	if err != nil {
		return apierr.NewAPIError(fiber.StatusBadRequest, "invalid vector parameter: "+err.Error()).Respond(c)
	}

	topK := 10
	if k := c.QueryInt("top_k", 10); k > 0 {
		topK = k
	}

	if vector != nil {
		results, err := search.Get().Search(c.Context(), search.SearchQuery{
			Vector:  vector,
			TopK:    topK,
			TeamIDs: []uuid.UUID{teamID},
			Status:  status,
		})
		if err != nil {
			if errors.Is(err, search.ErrVectorSearchNotSupported) {
				return apierr.NewAPIError(fiber.StatusBadRequest, "vector search not supported by the active search provider").Respond(c)
			}
			if errors.Is(err, search.ErrNotImplemented) {
				// Provider is a NoopProvider; fall through to the store layer below.
			} else {
				return apierr.ErrInternalError.Respond(c, err)
			}
		} else {
			if len(results) == 0 {
				return c.JSON(fiber.Map{"prompts": []*model.Prompt{}})
			}
			ids := make([]uuid.UUID, len(results))
			for i, r := range results {
				ids[i] = r.PromptID
			}
			prompts, err := store.GetPromptsByIDs(c.Context(), ids, []uuid.UUID{teamID}, status)
			if err != nil {
				return apierr.ErrInternalError.Respond(c, err)
			}
			return c.JSON(fiber.Map{"prompts": prompts})
		}
	} else if q := c.Query("q"); q != "" {
		results, err := search.Get().Search(c.Context(), search.SearchQuery{
			Q:       q,
			TeamIDs: []uuid.UUID{teamID},
			Status:  status,
		})
		if err != nil {
			if errors.Is(err, search.ErrNotImplemented) {
				// Provider is a NoopProvider; fall through to the store layer below.
			} else {
				return apierr.ErrInternalError.Respond(c, err)
			}
		} else {
			if len(results) == 0 {
				return c.JSON(fiber.Map{"prompts": []*model.Prompt{}})
			}
			ids := make([]uuid.UUID, len(results))
			for i, r := range results {
				ids[i] = r.PromptID
			}
			prompts, err := store.GetPromptsByIDs(c.Context(), ids, []uuid.UUID{teamID}, status)
			if err != nil {
				return apierr.ErrInternalError.Respond(c, err)
			}
			return c.JSON(fiber.Map{"prompts": prompts})
		}
	}

	prompts, err := store.ListPrompts(c.Context(), store.PromptFilter{
		TeamIDs:  []uuid.UUID{teamID},
		Archived: archived,
		Status:   status,
		Q:        c.Query("q"),
	})
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	if prompts == nil {
		prompts = []*model.Prompt{}
	}
	return c.JSON(fiber.Map{"prompts": prompts})
}

type updatePromptRequest struct {
	Description string `json:"description"`
}

func UpdatePrompt(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apierr.ErrInvalidPromptID.Respond(c)
	}

	teamIDs, err := getRequestTeamIDs(c)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	// First verify that the prompt exists and the user has basic team access
	if _, err := store.GetPromptByID(c.Context(), id, teamIDs); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrPromptNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	// Verify editor permissions
	editorTeamIDs, err := getRequestEditorTeamIDs(c)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	var req updatePromptRequest
	if err := c.BodyParser(&req); err != nil {
		return apierr.ErrInvalidRequestBody.Respond(c)
	}

	prompt, err := store.UpdatePrompt(c.Context(), id, editorTeamIDs, req.Description)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrForbidden.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	syncPromptSearchIndex(c.Context(), prompt)

	return c.JSON(fiber.Map{"prompt": prompt})
}

func DeletePrompt(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apierr.ErrInvalidPromptID.Respond(c)
	}

	teamIDs, err := getRequestTeamIDs(c)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	if _, err := store.GetPromptByID(c.Context(), id, teamIDs); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrPromptNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	editorTeamIDs, err := getRequestEditorTeamIDs(c)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	if err := store.DeletePrompt(c.Context(), id, editorTeamIDs); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrForbidden.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	search.Get().Deindex(c.Context(), id)

	return c.SendStatus(fiber.StatusNoContent)
}

func RestorePrompt(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apierr.ErrInvalidPromptID.Respond(c)
	}

	teamIDs, err := getRequestTeamIDs(c)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	if _, err := store.GetPromptByID(c.Context(), id, teamIDs); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrPromptNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	adminTeamIDs, err := getRequestAdminTeamIDs(c)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	if err := store.RestorePrompt(c.Context(), id, adminTeamIDs); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrForbidden.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	prompt, err := store.GetPromptByID(c.Context(), id, teamIDs)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	syncPromptSearchIndex(c.Context(), prompt)

	return c.JSON(fiber.Map{"prompt": prompt})
}

type movePromptRequest struct {
	TeamID string `json:"team_id"`
}

func MovePrompt(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apierr.ErrInvalidPromptID.Respond(c)
	}

	var req movePromptRequest
	if err := c.BodyParser(&req); err != nil {
		return apierr.ErrInvalidRequestBody.Respond(c)
	}

	targetTeamID, err := uuid.Parse(req.TeamID)
	if err != nil {
		return apierr.NewAPIError(fiber.StatusBadRequest, "invalid target team_id").Respond(c)
	}

	userID, ok := c.Locals(middleware.LocalsUserID).(uuid.UUID)
	if !ok || userID == uuid.Nil {
		return apierr.ErrUnauthorized.Respond(c)
	}

	// 1. Get the target team to find its OrgID
	targetTeam, err := store.GetTeamByID(c.Context(), targetTeamID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.NewAPIError(fiber.StatusNotFound, "target team not found").Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	// 2. Fetch the prompt to find its current team (we check basic read access)
	teamIDs, err := getRequestTeamIDs(c)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	prompt, err := store.GetPromptByID(c.Context(), id, teamIDs)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrPromptNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	// Get current team details
	currentTeam, err := store.GetTeamByID(c.Context(), prompt.TeamID)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	// 3. Verify Admin permission on BOTH current team and target team
	currentTeamAdmin := false
	if currentTeam.OrgID != nil {
		isOrgAdmin, err := store.IsOrgAdmin(c.Context(), userID, *currentTeam.OrgID)
		if err != nil {
			return apierr.ErrInternalError.Respond(c, err)
		}
		if isOrgAdmin {
			currentTeamAdmin = true
		}
	}
	if !currentTeamAdmin {
		isTeamAdmin, err := store.IsTeamAdmin(c.Context(), userID, currentTeam.ID)
		if err != nil {
			return apierr.ErrInternalError.Respond(c, err)
		}
		if isTeamAdmin {
			currentTeamAdmin = true
		}
	}

	targetTeamAdmin := false
	if targetTeam.OrgID != nil {
		isOrgAdmin, err := store.IsOrgAdmin(c.Context(), userID, *targetTeam.OrgID)
		if err != nil {
			return apierr.ErrInternalError.Respond(c, err)
		}
		if isOrgAdmin {
			targetTeamAdmin = true
		}
	}
	if !targetTeamAdmin {
		isTeamAdmin, err := store.IsTeamAdmin(c.Context(), userID, targetTeam.ID)
		if err != nil {
			return apierr.ErrInternalError.Respond(c, err)
		}
		if isTeamAdmin {
			targetTeamAdmin = true
		}
	}

	if !currentTeamAdmin || !targetTeamAdmin {
		return apierr.ErrForbidden.Respond(c)
	}

	// Move the prompt
	if err := store.MovePrompt(c.Context(), id, []uuid.UUID{currentTeam.ID}, targetTeamID); err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	// Retrieve updated prompt
	prompt, err = store.GetPromptByID(c.Context(), id, []uuid.UUID{targetTeamID})
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	syncPromptSearchIndex(c.Context(), prompt)

	return c.JSON(fiber.Map{"prompt": prompt})
}

func DiffVersions(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apierr.ErrInvalidPromptID.Respond(c)
	}

	fromVal := c.Query("from")
	toVal := c.Query("to")
	if fromVal == "" || toVal == "" {
		return apierr.NewAPIError(fiber.StatusBadRequest, "from and to query parameters are required").Respond(c)
	}

	from, err := strconv.Atoi(fromVal)
	if err != nil || from <= 0 {
		return apierr.NewAPIError(fiber.StatusBadRequest, "invalid from version").Respond(c)
	}

	to, err := strconv.Atoi(toVal)
	if err != nil || to <= 0 {
		return apierr.NewAPIError(fiber.StatusBadRequest, "invalid to version").Respond(c)
	}

	teamIDs, err := getRequestTeamIDs(c)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	// Verify read access to the prompt
	if _, err := store.GetPromptByID(c.Context(), id, teamIDs); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrPromptNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	fromVersion, err := store.GetVersion(c.Context(), id, from)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.NewAPIError(fiber.StatusNotFound, "from version not found").Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	toVersion, err := store.GetVersion(c.Context(), id, to)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.NewAPIError(fiber.StatusNotFound, "to version not found").Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	diff := difflib.UnifiedDiff{
		A:        difflib.SplitLines(fromVersion.Template),
		B:        difflib.SplitLines(toVersion.Template),
		FromFile: fmt.Sprintf("v%d", from),
		ToFile:   fmt.Sprintf("v%d", to),
		Context:  3,
	}
	diffText, err := difflib.GetUnifiedDiffString(diff)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	return c.JSON(fiber.Map{
		"from_version":  from,
		"to_version":    to,
		"from_template": fromVersion.Template,
		"to_template":   toVersion.Template,
		"diff":          diffText,
	})
}

// syncPromptSearchIndex is a helper to push the latest prompt state and tags to the search provider.
// It is called in a fire-and-forget manner during prompt mutations.
func syncPromptSearchIndex(ctx context.Context, prompt *model.Prompt) {
	tagMap, _ := store.GetTagsForPrompt(ctx, prompt.ID)
	uniqueTags := make(map[string]bool)
	for _, tags := range tagMap {
		for _, tag := range tags {
			uniqueTags[tag] = true
		}
	}
	var tagList []string
	for tag := range uniqueTags {
		tagList = append(tagList, tag)
	}

	_ = search.Get().Index(ctx, search.IndexablePrompt{
		ID:          prompt.ID,
		TeamID:      prompt.TeamID,
		Name:        prompt.Name,
		Description: prompt.Description,
		Slug:        prompt.Slug,
		Status:      prompt.Status,
		Tags:        tagList,
	})
}

// parsePromptStatusFilter normalizes status and archived query parameters into 
// a robust Status pointer (which universally propagates to FTS and DB fallbacks)
// and an Archived boolean (for store.ListPrompts backwards-compatibility).
func parsePromptStatusFilter(c *fiber.Ctx) (*string, *bool) {
	var status *string
	statusStr := c.Query("status")
	if statusStr != "" {
		status = &statusStr
	}

	var archived *bool
	archivedStr := c.Query("archived")
	if archivedStr != "" {
		if val, err := strconv.ParseBool(archivedStr); err == nil {
			archived = &val
		}
	}

	// If status is explicitly provided, it always wins (FTS and store both prioritize it).
	// If no status is provided, we must derive it from archived, or default to "active".
	if statusStr == "" {
		val := "active"
		if archived != nil && *archived {
			val = "archived"
		}
		status = &val
	}

	return status, archived
}
