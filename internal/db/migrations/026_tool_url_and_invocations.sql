-- Add url column to tools table
ALTER TABLE tools ADD COLUMN url VARCHAR(2048) NOT NULL DEFAULT '';

-- Create tool_invocations table for logging execution history
CREATE TABLE tool_invocations (
    id BIGSERIAL PRIMARY KEY,
    tool_id UUID NOT NULL REFERENCES tools(id) ON DELETE CASCADE,
    tool_version INTEGER NOT NULL,
    request_payload JSONB NOT NULL,
    response_payload JSONB,
    error TEXT,
    status_code INTEGER,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Indexing for fast paginated historical lookup
CREATE INDEX idx_tool_invocations_tool_id ON tool_invocations(tool_id);
CREATE INDEX idx_tool_invocations_created_at ON tool_invocations(created_at DESC);
