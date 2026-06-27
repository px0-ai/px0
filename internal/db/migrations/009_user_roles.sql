-- Add role column to team_members
ALTER TABLE team_members ADD COLUMN role VARCHAR(50) NOT NULL DEFAULT 'editor';
