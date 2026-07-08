-- Drop the existing global unique constraints on team_id+name and team_id+slug
ALTER TABLE prompts DROP CONSTRAINT IF EXISTS uq_prompts_team_name;
ALTER TABLE prompts DROP CONSTRAINT IF EXISTS uq_prompts_team_slug;

-- Create partial unique indexes that only apply when status is not 'archived'
CREATE UNIQUE INDEX uq_prompts_team_name_active ON prompts (team_id, name) WHERE status != 'archived';
CREATE UNIQUE INDEX uq_prompts_team_slug_active ON prompts (team_id, slug) WHERE status != 'archived';
