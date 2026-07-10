package store

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/px0-ai/px0/internal/db"
	"github.com/px0-ai/px0/internal/model"
)

type PromptFilter struct {
	TeamIDs  []uuid.UUID
	Tags     []string
	Archived *bool
	Status   *string
	Q        string
	Limit    *int
}

func CreatePrompt(ctx context.Context, teamID uuid.UUID, slug, name, description string) (*model.Prompt, error) {
	p := &model.Prompt{}
	err := db.Pool.QueryRow(ctx,
		`INSERT INTO prompts (team_id, slug, name, description)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id, team_id, slug, name, description, status, created_at, updated_at`,
		teamID, slug, name, description,
	).Scan(&p.ID, &p.TeamID, &p.Slug, &p.Name, &p.Description, &p.Status, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, ErrDuplicate
		}
		return nil, fmt.Errorf("create prompt: %w", err)
	}
	return p, nil
}

// minFTSScore is the minimum ts_rank below which a match is treated as
// noise. Empirically, single-token english matches score in the
// 0.05-0.30 range; multi-token queries score higher. Anything below
// this is coincidental substring overlap with no real lexical match,
// and returning it just inflates top_k with garbage.
//
// The threshold is a package-level constant rather than a query
// parameter so the behaviour is stable across callers and tests.
const minFTSScore = 0.05

// ListPrompts performs PostgreSQL full-text search against the
// `search_vector` tsvector column added in migration 020_prompts_fts.sql.
// Weights are A=name, B=description, C=slug, as set up by the trigger.
//
// The function short-circuits to an empty result when filter.Q is empty
// rather than relying on websearch_to_tsquery('') — the latter is
// implementation-defined in PostgreSQL and can match arbitrary tokens.
//
// A minimum ts_rank threshold filters out coincidental matches; queries
// with no real lexical overlap return an empty result set rather than
// being padded to top_k with weak hits. This fixes the over-inclusion
// behaviour of the previous ILIKE path, which returned every prompt
// whose name or description merely contained the query as a substring.
//
// Results are ordered by ts_rank descending, with p.created_at as a
// stable tiebreaker. Tags, status, team scope, and limit filters are
// honoured the same way they were in the original implementation.
func ListPrompts(ctx context.Context, filter PromptFilter) ([]*model.Prompt, error) {
	if filter.Q == "" {
		return []*model.Prompt{}, nil
	}

	args := []any{filter.Q, filter.TeamIDs}
	joins := ""
	whereClauses := []string{
		"p.team_id = ANY($2)",
		"p.search_vector @@ q.tsq",
	}

	if len(filter.Tags) > 0 {
		joins = " INNER JOIN prompt_tags pt ON pt.prompt_id = p.id"
		args = append(args, filter.Tags)
		whereClauses = append(whereClauses, fmt.Sprintf("pt.tag = ANY($%d)", len(args)))
	}

	if filter.Status != nil {
		args = append(args, *filter.Status)
		whereClauses = append(whereClauses, fmt.Sprintf("p.status = $%d", len(args)))
	} else if filter.Archived != nil {
		statusVal := model.PromptStatusActive
		if *filter.Archived {
			statusVal = model.PromptStatusArchived
		}
		args = append(args, statusVal)
		whereClauses = append(whereClauses, fmt.Sprintf("p.status = $%d", len(args)))
	}

	limitClause := ""
	if filter.Limit != nil {
		args = append(args, *filter.Limit)
		limitClause = fmt.Sprintf(" LIMIT $%d", len(args))
	}

	// Two-stage CTE: scored computes the rank once, filtered dedupes
	// (the tag join can multiply rows per id) and applies the minimum
	// rank threshold, the outer SELECT re-sorts by rank descending.
	// We can't fold ORDER BY into the DISTINCT statement because
	// PostgreSQL requires every ORDER BY expression to appear in the
	// SELECT list of a DISTINCT query, and we don't want to expose
	// `score` in the output.
	query := fmt.Sprintf(`
		WITH q AS (SELECT websearch_to_tsquery('english', $1) AS tsq),
		scored AS (
			SELECT p.id, p.team_id, p.slug, p.name, p.description, p.status, p.created_at, p.updated_at,
			       ts_rank(p.search_vector, q.tsq) AS score
			FROM prompts p%s, q
			WHERE %s
		),
		filtered AS (
			SELECT DISTINCT ON (id) *
			FROM scored
			WHERE score >= %f
			ORDER BY id, score DESC
		)
		SELECT id, team_id, slug, name, description, status, created_at, updated_at
		FROM filtered
		ORDER BY score DESC, created_at DESC%s
	`, joins, strings.Join(whereClauses, " AND "), minFTSScore, limitClause)

	rows, err := db.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list prompts: %w", err)
	}
	defer rows.Close()

	var prompts []*model.Prompt
	for rows.Next() {
		p := &model.Prompt{}
		if err := rows.Scan(&p.ID, &p.TeamID, &p.Slug, &p.Name, &p.Description, &p.Status, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		prompts = append(prompts, p)
	}
	return prompts, rows.Err()
}

// ListPromptsByFilter returns a non-search listing of prompts matching
// the given filter. This is the unranked path used by the list endpoints
// when the caller did not supply a ?q= query string.
//
// It is the unranked counterpart to ListPrompts: same filter surface
// (team scope, status, archived, tags, limit) but no FTS, no ranking,
// and no minimum-score threshold. The result is just a slice of prompts
// ordered by created_at descending.
func ListPromptsByFilter(ctx context.Context, filter PromptFilter) ([]*model.Prompt, error) {
	query := `SELECT DISTINCT p.id, p.team_id, p.slug, p.name, p.description, p.status, p.created_at, p.updated_at
			  FROM prompts p`
	args := []any{}
	joins := ""
	whereClauses := []string{}

	if len(filter.Tags) > 0 {
		joins += " INNER JOIN prompt_tags pt ON pt.prompt_id = p.id"
		args = append(args, filter.Tags)
		whereClauses = append(whereClauses, fmt.Sprintf("pt.tag = ANY($%d)", len(args)))
	}

	if len(filter.TeamIDs) > 0 {
		args = append(args, filter.TeamIDs)
		whereClauses = append(whereClauses, fmt.Sprintf("p.team_id = ANY($%d)", len(args)))
	}

	if filter.Status != nil {
		args = append(args, *filter.Status)
		whereClauses = append(whereClauses, fmt.Sprintf("p.status = $%d", len(args)))
	} else if filter.Archived != nil {
		statusVal := model.PromptStatusActive
		if *filter.Archived {
			statusVal = model.PromptStatusArchived
		}
		args = append(args, statusVal)
		whereClauses = append(whereClauses, fmt.Sprintf("p.status = $%d", len(args)))
	}

	query += joins
	if len(whereClauses) > 0 {
		query += " WHERE " + strings.Join(whereClauses, " AND ")
	}
	query += " ORDER BY p.created_at DESC"

	if filter.Limit != nil {
		args = append(args, *filter.Limit)
		query += fmt.Sprintf(" LIMIT $%d", len(args))
	}

	rows, err := db.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list prompts by filter: %w", err)
	}
	defer rows.Close()

	var prompts []*model.Prompt
	for rows.Next() {
		p := &model.Prompt{}
		if err := rows.Scan(&p.ID, &p.TeamID, &p.Slug, &p.Name, &p.Description, &p.Status, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		prompts = append(prompts, p)
	}
	return prompts, rows.Err()
}

func GetPromptByID(ctx context.Context, id uuid.UUID, teamIDs []uuid.UUID) (*model.Prompt, error) {
	p := &model.Prompt{}
	err := db.Pool.QueryRow(ctx,
		`SELECT id, team_id, slug, name, description, status, created_at, updated_at
		 FROM prompts
		 WHERE id = $1 AND team_id = ANY($2)`,
		id, teamIDs,
	).Scan(&p.ID, &p.TeamID, &p.Slug, &p.Name, &p.Description, &p.Status, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get prompt: %w", err)
	}
	return p, nil
}

func GetPromptBySlug(ctx context.Context, slug string, teamIDs []uuid.UUID) (*model.Prompt, error) {
	p := &model.Prompt{}
	err := db.Pool.QueryRow(ctx,
		`SELECT id, team_id, slug, name, description, status, created_at, updated_at
		 FROM prompts
		 WHERE slug = $1 AND team_id = ANY($2)`,
		slug, teamIDs,
	).Scan(&p.ID, &p.TeamID, &p.Slug, &p.Name, &p.Description, &p.Status, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get prompt by slug: %w", err)
	}
	return p, nil
}

func ArchivePrompt(ctx context.Context, id uuid.UUID, teamIDs []uuid.UUID) error {
	// First check if the prompt belongs to one of the provided teams
	_, err := GetPromptByID(ctx, id, teamIDs)
	if err != nil {
		return err // ErrNotFound if not found or no access
	}

	result, err := db.Pool.Exec(ctx, "UPDATE prompts SET status = 'archived' WHERE id = $1", id)
	if err != nil {
		return fmt.Errorf("archive prompt: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func UpdatePrompt(ctx context.Context, id uuid.UUID, teamIDs []uuid.UUID, description string) (*model.Prompt, error) {
	// First check if the prompt belongs to one of the allowed teams
	_, err := GetPromptByID(ctx, id, teamIDs)
	if err != nil {
		return nil, err // ErrNotFound if not found or no access
	}

	p := &model.Prompt{}
	err = db.Pool.QueryRow(ctx,
		`UPDATE prompts
		 SET description = $1, updated_at = NOW()
		 WHERE id = $2
		 RETURNING id, team_id, slug, name, description, status, created_at, updated_at`,
		description, id,
	).Scan(&p.ID, &p.TeamID, &p.Slug, &p.Name, &p.Description, &p.Status, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("update prompt: %w", err)
	}
	return p, nil
}

func RestorePrompt(ctx context.Context, id uuid.UUID, teamIDs []uuid.UUID) error {
	_, err := GetPromptByID(ctx, id, teamIDs)
	if err != nil {
		return err // ErrNotFound if not found or no access
	}

	result, err := db.Pool.Exec(ctx, "UPDATE prompts SET status = 'active' WHERE id = $1", id)
	if err != nil {
		return fmt.Errorf("restore prompt: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func DeletePrompt(ctx context.Context, id uuid.UUID, teamIDs []uuid.UUID) error {
	_, err := GetPromptByID(ctx, id, teamIDs)
	if err != nil {
		return err // ErrNotFound if not found or no access
	}

	result, err := db.Pool.Exec(ctx, "DELETE FROM prompts WHERE id = $1", id)
	if err != nil {
		return fmt.Errorf("delete prompt: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func MovePrompt(ctx context.Context, id uuid.UUID, teamIDs []uuid.UUID, targetTeamID uuid.UUID) error {
	_, err := GetPromptByID(ctx, id, teamIDs)
	if err != nil {
		return err // ErrNotFound if not found or no access
	}

	_, err = db.Pool.Exec(ctx, "UPDATE prompts SET team_id = $1, updated_at = NOW() WHERE id = $2", targetTeamID, id)
	if err != nil {
		return fmt.Errorf("move prompt: %w", err)
	}
	return nil
}

// GetPromptsByIDs fetches full prompt records for the given IDs, scoped to
// the provided teamIDs for tenancy isolation.
//
// An optional status filter is applied as a store-layer safety net — the
// search provider may already have filtered by status, but we re-apply it
// here for defense-in-depth correctness.
//
// The returned slice preserves the order of the input ids slice so that the
// caller's score-based ranking (from the search provider) is maintained.
func GetPromptsByIDs(ctx context.Context, ids []uuid.UUID, teamIDs []uuid.UUID, status *string) ([]*model.Prompt, error) {
	args := []any{ids, teamIDs}
	where := "id = ANY($1) AND team_id = ANY($2)"

	if status != nil {
		args = append(args, *status)
		where += fmt.Sprintf(" AND status = $%d", len(args))
	}

	rows, err := db.Pool.Query(ctx,
		fmt.Sprintf(`SELECT id, team_id, slug, name, description, status, created_at, updated_at
		             FROM prompts WHERE %s`, where),
		args...)
	if err != nil {
		return nil, fmt.Errorf("get prompts by ids: %w", err)
	}
	defer rows.Close()

	byID := make(map[uuid.UUID]*model.Prompt, len(ids))
	for rows.Next() {
		p := &model.Prompt{}
		if err := rows.Scan(&p.ID, &p.TeamID, &p.Slug, &p.Name, &p.Description,
			&p.Status, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		byID[p.ID] = p
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Re-order to match the original ids slice (score order from search provider).
	result := make([]*model.Prompt, 0, len(ids))
	for _, id := range ids {
		if p, ok := byID[id]; ok {
			result = append(result, p)
		}
	}
	return result, nil
}

