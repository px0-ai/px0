CREATE TABLE prompt_versions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    prompt_id UUID NOT NULL REFERENCES prompts(id) ON DELETE CASCADE,
    version INTEGER NOT NULL,
    template TEXT NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'live', 'archived')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    published_at TIMESTAMPTZ,
    UNIQUE(prompt_id, version)
);

CREATE INDEX idx_prompt_versions_prompt_id ON prompt_versions(prompt_id);
CREATE INDEX idx_prompt_versions_prompt_status ON prompt_versions(prompt_id, status);
