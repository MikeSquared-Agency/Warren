-- Token usage tracking for the prompt optimisation loop.
-- Applied by Warren on startup via store.EnsureSchema().

CREATE TABLE IF NOT EXISTS token_usage (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id       TEXT NOT NULL UNIQUE,
    session_key      TEXT,
    task_id          TEXT,
    agent_id         TEXT,
    session_label    TEXT,
    provider         TEXT DEFAULT 'anthropic',
    model_id         TEXT,
    model_tier       TEXT,
    input_tokens     BIGINT DEFAULT 0,
    output_tokens    BIGINT DEFAULT 0,
    cache_read_tokens  BIGINT DEFAULT 0,
    cache_write_tokens BIGINT DEFAULT 0,
    total_tokens     BIGINT DEFAULT 0,
    input_cost_usd      NUMERIC(12,8) DEFAULT 0,
    output_cost_usd     NUMERIC(12,8) DEFAULT 0,
    cache_read_cost_usd NUMERIC(12,8) DEFAULT 0,
    cache_write_cost_usd NUMERIC(12,8) DEFAULT 0,
    total_cost_usd      NUMERIC(12,8) DEFAULT 0,
    cost_usd         NUMERIC(12,8) DEFAULT 0,
    source           TEXT DEFAULT 'jsonl',
    request_count    INT DEFAULT 0,
    duration_ms      BIGINT,
    exit_code        INT,
    files_changed    TEXT[],
    mission_id       TEXT,
    first_seen_at    TIMESTAMPTZ DEFAULT NOW(),
    last_seen_at     TIMESTAMPTZ DEFAULT NOW(),
    completed_at     TIMESTAMPTZ,
    created_at       TIMESTAMPTZ DEFAULT NOW(),
    updated_at       TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_token_usage_agent_id ON token_usage(agent_id);
CREATE INDEX IF NOT EXISTS idx_token_usage_model_id ON token_usage(model_id);
CREATE INDEX IF NOT EXISTS idx_token_usage_first_seen_at ON token_usage(first_seen_at);
CREATE INDEX IF NOT EXISTS idx_token_usage_completed_at ON token_usage(completed_at);
CREATE INDEX IF NOT EXISTS idx_token_usage_task_id ON token_usage(task_id);
CREATE INDEX IF NOT EXISTS idx_token_usage_mission_id ON token_usage(mission_id);

CREATE TABLE IF NOT EXISTS model_pricing (
    model_id             TEXT PRIMARY KEY,
    provider             TEXT NOT NULL DEFAULT 'anthropic',
    input_per_mtok       NUMERIC(10,4) NOT NULL,
    output_per_mtok      NUMERIC(10,4) NOT NULL,
    cache_read_per_mtok  NUMERIC(10,4) DEFAULT 0,
    cache_write_per_mtok NUMERIC(10,4) DEFAULT 0,
    updated_at           TIMESTAMPTZ DEFAULT NOW()
);

INSERT INTO model_pricing (model_id, provider, input_per_mtok, output_per_mtok, cache_read_per_mtok, cache_write_per_mtok) VALUES
    ('claude-opus-4-6', 'anthropic', 15.0, 75.0, 1.5, 18.75),
    ('claude-sonnet-4-5-20250929', 'anthropic', 3.0, 15.0, 0.3, 3.75),
    ('claude-haiku-4-5-20251001', 'anthropic', 0.25, 1.25, 0.025, 0.3125),
    ('gpt-4o', 'openai', 2.5, 10.0, 0, 0),
    ('gpt-4o-mini', 'openai', 0.15, 0.6, 0, 0)
ON CONFLICT (model_id) DO NOTHING;
