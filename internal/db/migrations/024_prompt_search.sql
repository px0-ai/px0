ALTER TABLE prompts
    ADD COLUMN search_vector tsvector
    GENERATED ALWAYS AS (
        setweight(to_tsvector('english', coalesce(name, '')), 'A') ||
        setweight(to_tsvector('english', coalesce(description, '')), 'B') ||
        setweight(to_tsvector('english', coalesce(slug, '')), 'C')
    ) STORED;

CREATE INDEX idx_prompts_search_vector
    ON prompts USING GIN (search_vector);
