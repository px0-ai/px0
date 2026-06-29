CREATE TABLE prompt_tags (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    prompt_id UUID NOT NULL REFERENCES prompts(id) ON DELETE CASCADE,
    version INTEGER NOT NULL,
    tag VARCHAR(50) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_prompt_id_tag UNIQUE (prompt_id, tag),
    CONSTRAINT fk_prompt_tags_version FOREIGN KEY (prompt_id, version) REFERENCES prompt_versions(prompt_id, version) ON DELETE CASCADE
);

CREATE INDEX idx_prompt_tags_prompt_id ON prompt_tags(prompt_id);
