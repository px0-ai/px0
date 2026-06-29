-- Drop the existing check constraint on prompt_versions status
ALTER TABLE prompt_versions DROP CONSTRAINT IF EXISTS prompt_versions_status_check;

-- Add the check constraint with 'stable'
ALTER TABLE prompt_versions ADD CONSTRAINT prompt_versions_status_check CHECK (status IN ('draft', 'stable', 'live', 'archived'));
