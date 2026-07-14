ALTER TABLE prompt_versions
ADD COLUMN model TEXT,
ADD COLUMN model_params JSONB;
