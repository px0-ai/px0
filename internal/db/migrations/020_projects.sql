-- Projects: a named container for prompts that sits between Team and Prompt.
-- A project belongs to exactly one owning team and may grant access to
-- additional teams in the same organization (issue #12).

CREATE TABLE projects (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    owning_team_id UUID NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    name VARCHAR(255) NOT NULL,
    slug VARCHAR(255) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_projects_team_name UNIQUE (owning_team_id, name),
    CONSTRAINT uq_projects_team_slug UNIQUE (owning_team_id, slug)
);

-- Access grants: an additional team that may work with the project's prompts.
-- The owning team's access is implicit and is never stored here.
CREATE TABLE project_team_access (
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    team_id UUID NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    PRIMARY KEY (project_id, team_id)
);
