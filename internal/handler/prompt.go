package handler

import (
	"context"
	"errors"
	"fmt"
	"log"
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

	return listPromptsForTeam(c, teamID)
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

	_ = search.GetVector().Deindex(c.Context(), id)

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
		return c.JSON(fiber.Map{"prompts": []*model.Prompt{}, "engine": "fts"})
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

	return listPromptsForTeam(c, teamID)
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

	search.GetVector().Deindex(c.Context(), id)

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

	if err := search.GetVector().Index(ctx, search.IndexablePrompt{
		ID:          prompt.ID,
		TeamID:      prompt.TeamID,
		Name:        prompt.Name,
		Description: prompt.Description,
		Slug:        prompt.Slug,
		Status:      prompt.Status,
		Tags:        tagList,
	}); err != nil {
		log.Printf("warn: search index failed for prompt %s: %v", prompt.ID, err)
	}
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

// listPromptsForTeam is the shared, mode-aware implementation used by
// ListPrompts (team-scoped) and ListAllPrompts (cross-team). It reads the
// ?mode= query parameter and dispatches to the appropriate ranking engine.
//
// Mode routing:
//   - "fts" (default): real PostgreSQL FTS via the search provider when ?q=
//     is set, plain listing otherwise. Does not silently fall back to ILIKE.
//   - "vector": pre-computed ?vector= or an embedded ?q=. Embedder must be
//     configured; embedding failures yield 501 — never a silent alias to FTS.
//   - "hybrid": returns 501 (not yet implemented).
//
// For backward compatibility, callers that omit ?mode= but supply a raw
// ?vector= are routed to mode=vector (the historical behaviour). Callers
// that supply ?q= without ?mode= are routed to mode=fts.
//
// Every successful response includes an "engine" field identifying which
// path actually served the request, so clients (and tests) can verify the
// mode they asked for is the mode that ran.
func listPromptsForTeam(c *fiber.Ctx, teamID uuid.UUID) error {
	status, archived := parsePromptStatusFilter(c)

	mode, ok := parseSearchMode(c)
	if !ok {
		// parseSearchMode already wrote a 4xx response; return nil so
		// fiber doesn't run ErrorHandler on top of it.
		return nil
	}

	// Backward-compat: callers predating ?mode= may still send ?vector=
	// without ?mode=; treat that as an implicit mode=vector request.
	if mode == "fts" && strings.TrimSpace(c.Query("vector")) != "" {
		mode = "vector"
	}

	topK := 10
	if k := c.QueryInt("top_k", 10); k > 0 {
		topK = k
	}

	switch mode {
	case "fts":
		return runFTSMode(c, teamID, status, archived, topK)
	case "vector":
		return runVectorMode(c, teamID, status, topK)
	case "hybrid":
		return apierr.NewAPIError(fiber.StatusNotImplemented, "hybrid search not implemented").Respond(c)
	}
	return apierr.ErrInternalError.Respond(c, fmt.Errorf("unhandled search mode %q", mode))
}

// parseSearchMode reads ?mode= and validates it. An empty or missing
// value defaults to "fts". On invalid input, it writes a 400 response
// and returns ok=false so the caller can short-circuit with a nil
// return — returning a non-nil error here would cause fiber's
// ErrorHandler to attempt another write and potentially clobber the
// 400 with a 500.
func parseSearchMode(c *fiber.Ctx) (string, bool) {
	raw := strings.ToLower(strings.TrimSpace(c.Query("mode")))
	switch raw {
	case "":
		return "fts", true
	case "fts", "vector", "hybrid":
		return raw, true
	default:
		_ = apierr.NewAPIError(fiber.StatusBadRequest, "invalid mode: must be one of 'fts', 'vector', 'hybrid'").Respond(c)
		return "", false
	}
}

// runFTSMode handles mode=fts. With ?q= set, it routes to the FTS
// search provider (PostgreSQL FTS when that provider is active) via
// search.GetFTS().Search. Without ?q=, it returns a plain listing.
//
// Unlike the previous behaviour, this path never silently falls back to
// the ILIKE-based store.ListPrompts. A search provider that returns
// ErrNotImplemented or ErrVectorSearchNotSupported produces 501.
func runFTSMode(c *fiber.Ctx, teamID uuid.UUID, status *string, archived *bool, topK int) error {
	q := c.Query("q")
	if q == "" {
		return runPlainList(c, teamID, status, archived, topK)
	}

	results, err := search.GetFTS().Search(c.Context(), search.SearchQuery{
		Q:       q,
		TopK:    topK,
		TeamIDs: []uuid.UUID{teamID},
		Status:  status,
	})
	if err != nil {
		if errors.Is(err, search.ErrNotImplemented) {
			return apierr.NewAPIError(fiber.StatusNotImplemented, "fts search not implemented by the active search provider").Respond(c)
		}
		if errors.Is(err, search.ErrVectorSearchNotSupported) {
			// An active embedder auto-converted the FTS query into a vector
			// query and the underlying provider does not support vectors.
			// Surface the mismatch rather than aliasing to anything else.
			return apierr.NewAPIError(fiber.StatusNotImplemented, "fts search not available: the active search provider does not support FTS for this query").Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}
	return respondWithPromptIDs(c, results, teamID, status, "fts")
}

// runPlainList returns a non-search listing of prompts for the team,
// filtered by status/archived. Used by mode=fts when ?q= is empty.
func runPlainList(c *fiber.Ctx, teamID uuid.UUID, status *string, archived *bool, topK int) error {
	prompts, err := store.ListPromptsByFilter(c.Context(), store.PromptFilter{
		TeamIDs:  []uuid.UUID{teamID},
		Archived: archived,
		Status:   status,
		Limit:    &topK,
	})
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	if prompts == nil {
		prompts = []*model.Prompt{}
	}
	return c.JSON(fiber.Map{"prompts": prompts, "engine": "fts"})
}

// runVectorMode handles mode=vector. It resolves a query vector from
// ?vector= or, when only ?q= is provided, by asking the global embedder
// to embed the text. A missing or failing embedder yields 501 — no
// silent alias to FTS or to plain listing.
func runVectorMode(c *fiber.Ctx, teamID uuid.UUID, status *string, topK int) error {
	vector, ok := resolveVectorFromQuery(c)
	if !ok {
		// resolveVectorFromQuery already wrote a 4xx/5xx response;
		// return nil so fiber doesn't run ErrorHandler on top of it.
		return nil
	}
	if vector == nil {
		return apierr.NewAPIError(fiber.StatusBadRequest, "vector mode requires either vector= or q= parameter").Respond(c)
	}

	results, err := search.GetVector().Search(c.Context(), search.SearchQuery{
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
			return apierr.NewAPIError(fiber.StatusNotImplemented, "vector search not implemented by the active search provider").Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}
	return respondWithPromptIDs(c, results, teamID, status, "vector")
}

// resolveVectorFromQuery returns a query vector using, in order:
//  1. a pre-computed ?vector= parameter (no embedder required),
//  2. an embedded ?q= via the global embedder (require a working embedder),
//  3. (nil, true) when neither is supplied, so the caller can produce a
//     precise 400 explaining that vector mode needs a vector.
//
// On embedder absence or embed failure, it writes a 4xx/5xx response
// via apierr.Respond and returns ok=false so the caller can short-circuit
// with a nil return. Same rationale as parseSearchMode: returning a
// non-nil error from the handler would invoke fiber's ErrorHandler
// and potentially clobber the response already written by Respond.
func resolveVectorFromQuery(c *fiber.Ctx) ([]float32, bool) {
	vectorRaw := c.Query("vector")
	vector, err := parseVectorParam(vectorRaw)
	if err != nil {
		_ = apierr.NewAPIError(fiber.StatusBadRequest, "invalid vector parameter: "+err.Error()).Respond(c)
		return nil, false
	}
	if vector != nil {
		return vector, true
	}

	q := c.Query("q")
	if q == "" {
		return nil, true
	}

	embedder := search.GetEmbedder()
	if embedder == nil {
		_ = apierr.NewAPIError(fiber.StatusNotImplemented, "vector search unavailable: no embedder configured (set EMBEDDER_PROVIDER and HF_TOKEN)").Respond(c)
		return nil, false
	}

	embedded, err := embedder.Embed(c.Context(), q)
	if err != nil {
		_ = apierr.NewAPIError(fiber.StatusNotImplemented, "vector search unavailable: embedding failed: "+err.Error()).Respond(c)
		return nil, false
	}
	return embedded, true
}

// respondWithPromptIDs hydrates the given SearchResults into full Prompt
// records (preserving the provider's score order) and writes the standard
// {"prompts": [...], "engine": <engine>} JSON response. The "engine" field
// always reflects the path that produced the results, never the caller's
// requested mode, so clients can detect mismatches.
func respondWithPromptIDs(c *fiber.Ctx, results []search.SearchResult, teamID uuid.UUID, status *string, engine string) error {
	if len(results) == 0 {
		return c.JSON(fiber.Map{"prompts": []*model.Prompt{}, "engine": engine})
	}
	ids := make([]uuid.UUID, len(results))
	for i, r := range results {
		ids[i] = r.PromptID
	}
	prompts, err := store.GetPromptsByIDs(c.Context(), ids, []uuid.UUID{teamID}, status)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	return c.JSON(fiber.Map{"prompts": prompts, "engine": engine})
}
