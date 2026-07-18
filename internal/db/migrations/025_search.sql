-- Search documents are generated from entity metadata so writes never need a
-- separate indexing path. See docs/search.md for the weighting and index
-- rationale, query flow, and instructions for adding another entity type.

ALTER TABLE prompts
    ADD COLUMN search_vector tsvector
    GENERATED ALWAYS AS (
        setweight(to_tsvector('english', coalesce(name, '')), 'A') ||
        setweight(to_tsvector('english', coalesce(description, '')), 'B') ||
        setweight(to_tsvector('english', coalesce(slug, '')), 'C')
    ) STORED;

ALTER TABLE skills
    ADD COLUMN search_vector tsvector
    GENERATED ALWAYS AS (
        setweight(to_tsvector('english', coalesce(name, '')), 'A') ||
        setweight(to_tsvector('english', coalesce(description, '')), 'B') ||
        setweight(to_tsvector('english', coalesce(slug, '')), 'C')
    ) STORED;

ALTER TABLE tools
    ADD COLUMN search_vector tsvector
    GENERATED ALWAYS AS (
        setweight(to_tsvector('english', coalesce(name, '')), 'A') ||
        setweight(to_tsvector('english', coalesce(description, '')), 'B') ||
        setweight(to_tsvector('english', coalesce(slug, '')), 'C')
    ) STORED;

CREATE INDEX idx_prompts_search_vector ON prompts USING GIN (search_vector);
CREATE INDEX idx_skills_search_vector ON skills USING GIN (search_vector);
CREATE INDEX idx_tools_search_vector ON tools USING GIN (search_vector);
