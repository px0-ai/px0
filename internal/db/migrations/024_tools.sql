-- Create tools table
CREATE TABLE tools (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name VARCHAR(255) NOT NULL,
    slug VARCHAR(255) NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_tools_project_name UNIQUE (project_id, name),
    CONSTRAINT uq_tools_project_slug UNIQUE (project_id, slug)
);

-- Create tool_versions table
CREATE TABLE tool_versions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tool_id UUID NOT NULL REFERENCES tools(id) ON DELETE CASCADE,
    version INTEGER NOT NULL,
    input_schema JSONB NOT NULL DEFAULT '{}',
    output_schema JSONB NOT NULL DEFAULT '{}',
    status VARCHAR(50) NOT NULL DEFAULT 'draft',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_tool_versions_tool_num UNIQUE (tool_id, version),
    CONSTRAINT tool_versions_status_check CHECK (status IN ('draft', 'stable', 'live', 'archived'))
);

CREATE INDEX idx_tool_versions_tool_id ON tool_versions(tool_id);
CREATE INDEX idx_tool_versions_tool_status ON tool_versions(tool_id, status);
