-- Prompts move from belonging directly to a Team to belonging to a Project
-- (issue #12, breaking change). Existing prompts are backfilled into one
-- default project per team that owns prompts.

-- 1. Add the project reference (nullable during backfill). Deleting a project
--    cascades to its prompts.
ALTER TABLE prompts ADD COLUMN project_id UUID REFERENCES projects(id) ON DELETE CASCADE;

-- 2. Backfill: every team that owns at least one prompt gets exactly one default
--    project (owned by that team, name/slug derived from the team); its prompts
--    move into that project. Teams with no prompts get no project.
DO $$
DECLARE
    t RECORD;
    new_project_id UUID;
BEGIN
    FOR t IN
        SELECT tm.id AS team_id, tm.name AS team_name
        FROM teams tm
        WHERE EXISTS (SELECT 1 FROM prompts p WHERE p.team_id = tm.id)
    LOOP
        INSERT INTO projects (owning_team_id, name, slug)
        VALUES (
            t.team_id,
            t.team_name || ' Default Project',
            COALESCE(
                NULLIF(trim(both '_' from regexp_replace(lower(t.team_name || ' default project'), '[^a-z0-9_]+', '_', 'g')), ''),
                'default_project'
            )
        )
        RETURNING id INTO new_project_id;

        UPDATE prompts SET project_id = new_project_id WHERE team_id = t.team_id;
    END LOOP;
END $$;

-- 3. Any prompt still without a project is an invalid orphan; drop it before
--    enforcing NOT NULL (defensive - migration 013 already forbids null team_id).
DELETE FROM prompts WHERE project_id IS NULL;

-- 4. Swap team-scoped uniqueness for project-scoped uniqueness and drop team_id.
ALTER TABLE prompts DROP CONSTRAINT IF EXISTS uq_prompts_team_name;
ALTER TABLE prompts DROP CONSTRAINT IF EXISTS uq_prompts_team_slug;

ALTER TABLE prompts ALTER COLUMN project_id SET NOT NULL;
ALTER TABLE prompts DROP COLUMN team_id;

ALTER TABLE prompts ADD CONSTRAINT uq_prompts_project_name UNIQUE (project_id, name);
ALTER TABLE prompts ADD CONSTRAINT uq_prompts_project_slug UNIQUE (project_id, slug);
