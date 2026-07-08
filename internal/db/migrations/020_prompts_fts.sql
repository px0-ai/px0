-- Add a pre-computed tsvector column for full-text search.
-- Weights: name=A (highest), description=B, slug=C (lowest).
ALTER TABLE prompts ADD COLUMN IF NOT EXISTS search_vector tsvector;

-- Back-fill for existing rows.
UPDATE prompts
SET search_vector =
    setweight(to_tsvector('english', coalesce(name, '')), 'A') ||
    setweight(to_tsvector('english', coalesce(description, '')), 'B') ||
    setweight(to_tsvector('english', coalesce(slug, '')), 'C');

-- GIN index for fast FTS queries.
CREATE INDEX IF NOT EXISTS idx_prompts_search_vector ON prompts USING GIN (search_vector);

-- Trigger function: keeps search_vector in sync on every INSERT or UPDATE.
CREATE OR REPLACE FUNCTION prompts_search_vector_update() RETURNS trigger AS $$
BEGIN
    NEW.search_vector :=
        setweight(to_tsvector('english', coalesce(NEW.name, '')), 'A') ||
        setweight(to_tsvector('english', coalesce(NEW.description, '')), 'B') ||
        setweight(to_tsvector('english', coalesce(NEW.slug, '')), 'C');
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE OR REPLACE TRIGGER trg_prompts_search_vector
BEFORE INSERT OR UPDATE ON prompts
FOR EACH ROW EXECUTE FUNCTION prompts_search_vector_update();
