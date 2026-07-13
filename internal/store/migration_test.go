package store_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/px0-ai/px0/internal/db"
	"github.com/px0-ai/px0/internal/store"
	"github.com/px0-ai/px0/internal/testutil"
)

// TestMigration021_BackfillDefaultProjects validates the core backfill algorithm
// from migration 021 (internal/db/migrations/021_prompts_to_projects.sql): every
// team that owns at least one prompt gets exactly one default project with its
// prompts moved in, and teams with no prompts get none.
//
// It reconstructs the pre-migration shape — prompts referencing a team, not yet
// a project — in a temporary table and runs the same one-project-per-team logic
// against the real teams/projects tables, then asserts the backfilled state.
func TestMigration021_BackfillDefaultProjects(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()

	withPrompts, err := store.CreateTeam(ctx, "Team With Prompts")
	require.NoError(t, err)
	withoutPrompts, err := store.CreateTeam(ctx, "Team Without Prompts")
	require.NoError(t, err)

	tx, err := db.Pool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(ctx) //nolint:errcheck

	_, err = tx.Exec(ctx, `CREATE TEMP TABLE legacy_prompts (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		team_id UUID NOT NULL,
		name TEXT NOT NULL,
		slug TEXT NOT NULL,
		project_id UUID
	) ON COMMIT DROP`)
	require.NoError(t, err)

	_, err = tx.Exec(ctx,
		`INSERT INTO legacy_prompts (team_id, name, slug) VALUES ($1, 'A', 'a'), ($1, 'B', 'b')`,
		withPrompts.ID)
	require.NoError(t, err)

	// Backfill logic mirroring migration 021.
	_, err = tx.Exec(ctx, `DO $$
	DECLARE
		t RECORD;
		new_project_id UUID;
	BEGIN
		FOR t IN
			SELECT tm.id AS team_id, tm.name AS team_name
			FROM teams tm
			WHERE EXISTS (SELECT 1 FROM legacy_prompts p WHERE p.team_id = tm.id)
		LOOP
			INSERT INTO projects (owning_team_id, name, slug)
			VALUES (
				t.team_id,
				t.team_name || ' Default Project',
				COALESCE(NULLIF(trim(both '_' from regexp_replace(lower(t.team_name || ' default project'), '[^a-z0-9_]+', '_', 'g')), ''), 'default_project')
			)
			RETURNING id INTO new_project_id;
			UPDATE legacy_prompts SET project_id = new_project_id WHERE team_id = t.team_id;
		END LOOP;
	END $$`)
	require.NoError(t, err)

	// Team with prompts: exactly one project was created.
	var withProjectCount int
	require.NoError(t, tx.QueryRow(ctx,
		`SELECT COUNT(*) FROM projects WHERE owning_team_id = $1`, withPrompts.ID).Scan(&withProjectCount))
	assert.Equal(t, 1, withProjectCount)

	// Both of its prompts were reassigned to that project; none left unassigned.
	var unassigned int
	require.NoError(t, tx.QueryRow(ctx,
		`SELECT COUNT(*) FROM legacy_prompts WHERE project_id IS NULL`).Scan(&unassigned))
	assert.Equal(t, 0, unassigned)

	var assignedToTeamProject int
	require.NoError(t, tx.QueryRow(ctx,
		`SELECT COUNT(*) FROM legacy_prompts lp
		 JOIN projects pr ON pr.id = lp.project_id
		 WHERE pr.owning_team_id = $1`, withPrompts.ID).Scan(&assignedToTeamProject))
	assert.Equal(t, 2, assignedToTeamProject)

	// Team without prompts: no project created.
	var withoutProjectCount int
	require.NoError(t, tx.QueryRow(ctx,
		`SELECT COUNT(*) FROM projects WHERE owning_team_id = $1`, withoutPrompts.ID).Scan(&withoutProjectCount))
	assert.Equal(t, 0, withoutProjectCount)
}
