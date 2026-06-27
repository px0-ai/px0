-- API Key Scoping
ALTER TABLE api_keys ADD COLUMN org_id UUID REFERENCES organizations(id) ON DELETE CASCADE;
ALTER TABLE api_keys ALTER COLUMN team_id DROP NOT NULL;
ALTER TABLE api_keys ADD COLUMN operation VARCHAR(50) NOT NULL DEFAULT 'read_render';

CREATE TABLE api_key_teams (
    api_key_id UUID NOT NULL REFERENCES api_keys(id) ON DELETE CASCADE,
    team_id UUID NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    PRIMARY KEY (api_key_id, team_id)
);
