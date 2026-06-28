ALTER TABLE prompts ADD COLUMN IF NOT EXISTS team_id UUID REFERENCES teams(id) ON DELETE CASCADE;
ALTER TABLE prompts ADD COLUMN IF NOT EXISTS slug VARCHAR(255);

-- Migrate existing prompts (if any) to their respective team_id
DO $$
BEGIN
    IF EXISTS (SELECT FROM information_schema.tables WHERE table_name = 'prompt_teams') THEN
        UPDATE prompts p SET team_id = (SELECT team_id FROM prompt_teams pt WHERE pt.prompt_id = p.id LIMIT 1) WHERE p.team_id IS NULL;
    END IF;
END $$;

-- Delete any prompts that do not have a team associated (invalid orphans)
DELETE FROM prompts WHERE team_id IS NULL;

-- Now enforce NOT NULL constraint on team_id
ALTER TABLE prompts ALTER COLUMN team_id SET NOT NULL;

-- Populate slug from name: trim, convert to lower case, replace non-alphanumeric with underscores, and clean up any extra underscores.
UPDATE prompts SET slug = regexp_replace(lower(trim(name)), '[^a-z0-9_]+', '_', 'g') WHERE slug IS NULL OR slug = '';
UPDATE prompts SET slug = trim(both '_' from slug) WHERE slug LIKE '%_%';

-- If slug is empty or null, generate a fallback using the id to ensure it's not empty
UPDATE prompts SET slug = 'prompt_' || id::text WHERE slug = '' OR slug IS NULL;

-- Enforce NOT NULL constraint on slug
ALTER TABLE prompts ALTER COLUMN slug SET NOT NULL;

-- Enforce unique constraints within a team
ALTER TABLE prompts DROP CONSTRAINT IF EXISTS uq_prompts_team_name;
ALTER TABLE prompts ADD CONSTRAINT uq_prompts_team_name UNIQUE (team_id, name);

ALTER TABLE prompts DROP CONSTRAINT IF EXISTS uq_prompts_team_slug;
ALTER TABLE prompts ADD CONSTRAINT uq_prompts_team_slug UNIQUE (team_id, slug);

-- Drop the redundant prompt_teams table
DROP TABLE IF EXISTS prompt_teams;
