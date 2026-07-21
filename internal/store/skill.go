package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/px0-ai/px0/internal/db"
	"github.com/px0-ai/px0/internal/model"
)

// CreateSkill inserts a skill owned by a project and automatically initializes Version 1 as a draft.
func CreateSkill(ctx context.Context, projectID uuid.UUID, slug, name, description string) (*model.Skill, error) {
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	s := &model.Skill{}
	err = tx.QueryRow(ctx,
		`INSERT INTO skills (project_id, slug, name, description)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id, project_id, slug, name, description, created_at, updated_at`,
		projectID, slug, name, description,
	).Scan(&s.ID, &s.ProjectID, &s.Slug, &s.Name, &s.Description, &s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, ErrDuplicate
		}
		return nil, fmt.Errorf("create skill: %w", err)
	}

	// Create skill version 1 as 'draft'
	_, err = tx.Exec(ctx,
		`INSERT INTO skill_versions (skill_id, version, status)
		 VALUES ($1, 1, 'draft')`,
		s.ID,
	)
	if err != nil {
		return nil, fmt.Errorf("create skill version 1: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit tx: %w", err)
	}

	return s, nil
}

// ListSkills returns all skills in the given projects.
func ListSkills(ctx context.Context, projectIDs []uuid.UUID) ([]*model.Skill, error) {
	if len(projectIDs) == 0 {
		return []*model.Skill{}, nil
	}

	rows, err := db.Pool.Query(ctx,
		`SELECT id, project_id, slug, name, description, created_at, updated_at
		 FROM skills
		 WHERE project_id = ANY($1)
		 ORDER BY created_at DESC`,
		projectIDs,
	)
	if err != nil {
		return nil, fmt.Errorf("list skills: %w", err)
	}
	defer rows.Close()

	var skills []*model.Skill
	for rows.Next() {
		s := &model.Skill{}
		err := rows.Scan(&s.ID, &s.ProjectID, &s.Slug, &s.Name, &s.Description, &s.CreatedAt, &s.UpdatedAt)
		if err != nil {
			return nil, err
		}
		skills = append(skills, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return skills, nil
}

// GetSkillByID retrieves a skill by its ID, checking project access.
func GetSkillByID(ctx context.Context, id uuid.UUID, projectIDs []uuid.UUID) (*model.Skill, error) {
	s := &model.Skill{}
	err := db.Pool.QueryRow(ctx,
		`SELECT id, project_id, slug, name, description, created_at, updated_at
		 FROM skills
		 WHERE id = $1 AND project_id = ANY($2)`,
		id, projectIDs,
	).Scan(&s.ID, &s.ProjectID, &s.Slug, &s.Name, &s.Description, &s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get skill by id: %w", err)
	}
	return s, nil
}

// GetSkillBySlug retrieves a skill by its slug, checking project access.
func GetSkillBySlug(ctx context.Context, slug string, projectIDs []uuid.UUID) (*model.Skill, error) {
	s := &model.Skill{}
	err := db.Pool.QueryRow(ctx,
		`SELECT id, project_id, slug, name, description, created_at, updated_at
		 FROM skills
		 WHERE slug = $1 AND project_id = ANY($2)`,
		slug, projectIDs,
	).Scan(&s.ID, &s.ProjectID, &s.Slug, &s.Name, &s.Description, &s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get skill by slug: %w", err)
	}
	return s, nil
}

// UpdateSkill updates skill metadata.
func UpdateSkill(ctx context.Context, id uuid.UUID, projectIDs []uuid.UUID, slug, name, description string) (*model.Skill, error) {
	s := &model.Skill{}
	err := db.Pool.QueryRow(ctx,
		`UPDATE skills
		 SET slug = $1, name = $2, description = $3, updated_at = NOW()
		 WHERE id = $4 AND project_id = ANY($5)
		 RETURNING id, project_id, slug, name, description, created_at, updated_at`,
		slug, name, description, id, projectIDs,
	).Scan(&s.ID, &s.ProjectID, &s.Slug, &s.Name, &s.Description, &s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, ErrDuplicate
		}
		return nil, fmt.Errorf("update skill: %w", err)
	}
	return s, nil
}

// DeleteSkill deletes a skill, cascading to versions and files.
func DeleteSkill(ctx context.Context, id uuid.UUID, projectIDs []uuid.UUID) error {
	res, err := db.Pool.Exec(ctx,
		`DELETE FROM skills WHERE id = $1 AND project_id = ANY($2)`,
		id, projectIDs,
	)
	if err != nil {
		return fmt.Errorf("delete skill: %w", err)
	}
	if res.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// CreateSkillVersion creates a new draft version of a skill.
func CreateSkillVersion(ctx context.Context, skillID uuid.UUID) (*model.SkillVersion, error) {
	v := &model.SkillVersion{}
	err := db.Pool.QueryRow(ctx,
		`WITH next_version AS (
			SELECT COALESCE(MAX(version), 0) + 1 AS ver
			FROM skill_versions
			WHERE skill_id = $1
		)
		INSERT INTO skill_versions (skill_id, version, status)
		SELECT $1, ver, 'draft'
		FROM next_version
		RETURNING id, skill_id, version, status, created_at, updated_at`,
		skillID,
	).Scan(&v.ID, &v.SkillID, &v.Version, &v.Status, &v.CreatedAt, &v.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("create skill version: %w", err)
	}
	return v, nil
}

// DuplicateSkillVersion copies a specific version to a new draft version.
func DuplicateSkillVersion(ctx context.Context, skillID uuid.UUID, sourceVersionNum int) (*model.SkillVersion, error) {
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var sourceID uuid.UUID
	err = tx.QueryRow(ctx,
		`SELECT id FROM skill_versions WHERE skill_id = $1 AND version = $2`,
		skillID, sourceVersionNum,
	).Scan(&sourceID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("find source version: %w", err)
	}

	v := &model.SkillVersion{}
	err = tx.QueryRow(ctx,
		`WITH next_version AS (
			SELECT COALESCE(MAX(version), 0) + 1 AS ver
			FROM skill_versions
			WHERE skill_id = $1
		)
		INSERT INTO skill_versions (skill_id, version, status)
		SELECT $1, ver, 'draft'
		FROM next_version
		RETURNING id, skill_id, version, status, created_at, updated_at`,
		skillID,
	).Scan(&v.ID, &v.SkillID, &v.Version, &v.Status, &v.CreatedAt, &v.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("create duplicated skill version: %w", err)
	}

	// Copy files
	_, err = tx.Exec(ctx,
		`INSERT INTO skill_files (skill_id, version_id, file_path, content)
		 SELECT skill_id, $1, file_path, content
		 FROM skill_files
		 WHERE version_id = $2`,
		v.ID, sourceID,
	)
	if err != nil {
		return nil, fmt.Errorf("copy files for duplicated version: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit tx: %w", err)
	}

	return v, nil
}

// ListSkillVersions lists all versions for a skill.
func ListSkillVersions(ctx context.Context, skillID uuid.UUID) ([]*model.SkillVersion, error) {
	rows, err := db.Pool.Query(ctx,
		`SELECT id, skill_id, version, status, created_at, updated_at
		 FROM skill_versions
		 WHERE skill_id = $1
		 ORDER BY version DESC`,
		skillID,
	)
	if err != nil {
		return nil, fmt.Errorf("list skill versions: %w", err)
	}
	defer rows.Close()

	var versions []*model.SkillVersion
	for rows.Next() {
		v := &model.SkillVersion{}
		err := rows.Scan(&v.ID, &v.SkillID, &v.Version, &v.Status, &v.CreatedAt, &v.UpdatedAt)
		if err != nil {
			return nil, err
		}
		versions = append(versions, v)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return versions, nil
}

// GetSkillVersion retrieves a specific version of a skill.
func GetSkillVersion(ctx context.Context, skillID uuid.UUID, versionNum int) (*model.SkillVersion, error) {
	v := &model.SkillVersion{}
	err := db.Pool.QueryRow(ctx,
		`SELECT id, skill_id, version, status, created_at, updated_at
		 FROM skill_versions
		 WHERE skill_id = $1 AND version = $2`,
		skillID, versionNum,
	).Scan(&v.ID, &v.SkillID, &v.Version, &v.Status, &v.CreatedAt, &v.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get skill version: %w", err)
	}
	return v, nil
}

// DeleteSkillVersion deletes a specific draft version of a skill.
func DeleteSkillVersion(ctx context.Context, skillID uuid.UUID, versionNum int) error {
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var status string
	err = tx.QueryRow(ctx,
		`SELECT status FROM skill_versions WHERE skill_id = $1 AND version = $2 FOR UPDATE`,
		skillID, versionNum,
	).Scan(&status)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("lock skill version: %w", err)
	}

	if status != "draft" {
		return ErrConflict // only drafts can be deleted
	}

	_, err = tx.Exec(ctx,
		`DELETE FROM skill_versions WHERE skill_id = $1 AND version = $2`,
		skillID, versionNum,
	)
	if err != nil {
		return fmt.Errorf("delete skill version: %w", err)
	}

	return tx.Commit(ctx)
}

// PromoteSkillVersion promotes a skill version status: draft -> stable -> live.
func PromoteSkillVersion(ctx context.Context, skillID uuid.UUID, versionNum int) (*model.SkillVersion, error) {
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var currentStatus string
	err = tx.QueryRow(ctx,
		`SELECT status FROM skill_versions WHERE skill_id = $1 AND version = $2 FOR UPDATE`,
		skillID, versionNum,
	).Scan(&currentStatus)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("lock skill version: %w", err)
	}

	var nextStatus string
	if currentStatus == "draft" {
		nextStatus = "stable"
	} else if currentStatus == "stable" {
		nextStatus = "live"
	} else {
		return nil, fmt.Errorf("cannot promote skill version in %s status: %w", currentStatus, ErrConflict)
	}

	// Demote existing live version if next is live
	if nextStatus == "live" {
		_, err = tx.Exec(ctx,
			`UPDATE skill_versions SET status = 'stable', updated_at = NOW()
			 WHERE skill_id = $1 AND status = 'live'`,
			skillID,
		)
		if err != nil {
			return nil, fmt.Errorf("demote existing live version: %w", err)
		}
	}

	v := &model.SkillVersion{}
	err = tx.QueryRow(ctx,
		`UPDATE skill_versions
		 SET status = $1, updated_at = NOW()
		 WHERE skill_id = $2 AND version = $3
		 RETURNING id, skill_id, version, status, created_at, updated_at`,
		nextStatus, skillID, versionNum,
	).Scan(&v.ID, &v.SkillID, &v.Version, &v.Status, &v.CreatedAt, &v.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("update promoted skill version: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit tx: %w", err)
	}

	return v, nil
}

// DemoteSkillVersion demotes a live skill version back to stable.
func DemoteSkillVersion(ctx context.Context, skillID uuid.UUID, versionNum int) (*model.SkillVersion, error) {
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var status string
	err = tx.QueryRow(ctx,
		`SELECT status FROM skill_versions WHERE skill_id = $1 AND version = $2 FOR UPDATE`,
		skillID, versionNum,
	).Scan(&status)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("lock skill version: %w", err)
	}

	if status != "live" {
		return nil, fmt.Errorf("only live versions can be demoted: %w", ErrConflict)
	}

	v := &model.SkillVersion{}
	err = tx.QueryRow(ctx,
		`UPDATE skill_versions
		 SET status = 'stable', updated_at = NOW()
		 WHERE skill_id = $1 AND version = $2
		 RETURNING id, skill_id, version, status, created_at, updated_at`,
		skillID, versionNum,
	).Scan(&v.ID, &v.SkillID, &v.Version, &v.Status, &v.CreatedAt, &v.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("demote skill version: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit tx: %w", err)
	}

	return v, nil
}

// ArchiveSkillVersion sets version status to archived.
func ArchiveSkillVersion(ctx context.Context, skillID uuid.UUID, versionNum int) (*model.SkillVersion, error) {
	v := &model.SkillVersion{}
	err := db.Pool.QueryRow(ctx,
		`UPDATE skill_versions
		 SET status = 'archived', updated_at = NOW()
		 WHERE skill_id = $1 AND version = $2
		 RETURNING id, skill_id, version, status, created_at, updated_at`,
		skillID, versionNum,
	).Scan(&v.ID, &v.SkillID, &v.Version, &v.Status, &v.CreatedAt, &v.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("archive skill version: %w", err)
	}
	return v, nil
}

// GetSkillFiles retrieves all files in a specific version.
func GetSkillFiles(ctx context.Context, versionID uuid.UUID) ([]model.SkillFile, error) {
	rows, err := db.Pool.Query(ctx,
		`SELECT id, skill_id, version_id, file_path, content, created_at, updated_at
		 FROM skill_files
		 WHERE version_id = $1
		 ORDER BY file_path ASC`,
		versionID,
	)
	if err != nil {
		return nil, fmt.Errorf("get skill files: %w", err)
	}
	defer rows.Close()

	var files []model.SkillFile
	for rows.Next() {
		var f model.SkillFile
		err := rows.Scan(&f.ID, &f.SkillID, &f.VersionID, &f.FilePath, &f.Content, &f.CreatedAt, &f.UpdatedAt)
		if err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return files, nil
}

// GetSkillFile retrieves a specific file by path in a version.
func GetSkillFile(ctx context.Context, versionID uuid.UUID, filePath string) (*model.SkillFile, error) {
	var f model.SkillFile
	err := db.Pool.QueryRow(ctx,
		`SELECT id, skill_id, version_id, file_path, content, created_at, updated_at
		 FROM skill_files
		 WHERE version_id = $1 AND file_path = $2`,
		versionID, filePath,
	).Scan(&f.ID, &f.SkillID, &f.VersionID, &f.FilePath, &f.Content, &f.CreatedAt, &f.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get skill file: %w", err)
	}
	return &f, nil
}

// ReplaceSkillFiles replaces all files in a draft version with a new set of files.
func ReplaceSkillFiles(ctx context.Context, versionID uuid.UUID, skillID uuid.UUID, files []model.SkillFile) error {
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Verify it's a draft
	var status string
	err = tx.QueryRow(ctx,
		`SELECT status FROM skill_versions WHERE id = $1 FOR UPDATE`,
		versionID,
	).Scan(&status)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("lock skill version for replace: %w", err)
	}
	if status != "draft" {
		return fmt.Errorf("can only replace files in draft version: %w", ErrConflict)
	}

	// Delete existing files
	_, err = tx.Exec(ctx, `DELETE FROM skill_files WHERE version_id = $1`, versionID)
	if err != nil {
		return fmt.Errorf("delete old skill files: %w", err)
	}

	// Insert new files
	for _, f := range files {
		_, err = tx.Exec(ctx,
			`INSERT INTO skill_files (skill_id, version_id, file_path, content)
			 VALUES ($1, $2, $3, $4)`,
			skillID, versionID, f.FilePath, f.Content,
		)
		if err != nil {
			return fmt.Errorf("insert skill file %s: %w", f.FilePath, err)
		}
	}

	return tx.Commit(ctx)
}

// UpsertSkillFile inserts or updates a file in a draft version.
func UpsertSkillFile(ctx context.Context, versionID uuid.UUID, skillID uuid.UUID, filePath string, content []byte) error {
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Verify draft status
	var status string
	err = tx.QueryRow(ctx,
		`SELECT status FROM skill_versions WHERE id = $1 FOR UPDATE`,
		versionID,
	).Scan(&status)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("lock skill version for upsert: %w", err)
	}
	if status != "draft" {
		return fmt.Errorf("can only upsert files in draft version: %w", ErrConflict)
	}

	_, err = tx.Exec(ctx,
		`INSERT INTO skill_files (skill_id, version_id, file_path, content)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (version_id, file_path) DO UPDATE
		 SET content = EXCLUDED.content, updated_at = NOW()`,
		skillID, versionID, filePath, content,
	)
	if err != nil {
		return fmt.Errorf("upsert skill file: %w", err)
	}

	return tx.Commit(ctx)
}

// DeleteSkillFile deletes a specific file in a draft version.
func DeleteSkillFile(ctx context.Context, versionID uuid.UUID, filePath string) error {
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Verify draft status
	var status string
	err = tx.QueryRow(ctx,
		`SELECT status FROM skill_versions WHERE id = $1 FOR UPDATE`,
		versionID,
	).Scan(&status)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("lock skill version for file delete: %w", err)
	}
	if status != "draft" {
		return fmt.Errorf("can only delete files in draft version: %w", ErrConflict)
	}

	res, err := tx.Exec(ctx,
		`DELETE FROM skill_files WHERE version_id = $1 AND file_path = $2`,
		versionID, filePath,
	)
	if err != nil {
		return fmt.Errorf("delete skill file: %w", err)
	}
	if res.RowsAffected() == 0 {
		return ErrNotFound
	}

	return tx.Commit(ctx)
}
