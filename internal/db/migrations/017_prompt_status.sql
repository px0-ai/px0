-- Migration to replace is_archived with status on prompts table
ALTER TABLE prompts ADD COLUMN IF NOT EXISTS status VARCHAR(50) NOT NULL DEFAULT 'active';

-- Migrate existing data if any
UPDATE prompts SET status = 'archived' WHERE is_archived = TRUE;

-- Drop the old is_archived column
ALTER TABLE prompts DROP COLUMN IF EXISTS is_archived;
