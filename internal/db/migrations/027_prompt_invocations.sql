CREATE TABLE IF NOT EXISTS prompt_invocations (
    id BIGSERIAL PRIMARY KEY,
    prompt_id UUID NOT NULL REFERENCES prompts(id) ON DELETE CASCADE,
    version INT NOT NULL,
    variables JSONB NOT NULL DEFAULT '{}'::jsonb,
    rendered_prompt TEXT NOT NULL,
    model_response TEXT,
    latency_ms INT,
    token_usage JSONB,
    cache_hit BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_prompt_invocations_prompt_id ON prompt_invocations(prompt_id);
CREATE INDEX IF NOT EXISTS idx_prompt_invocations_created_at ON prompt_invocations(created_at);