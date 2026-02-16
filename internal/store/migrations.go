package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

const schema = `
CREATE TABLE IF NOT EXISTS token_usage (
    id                  BIGSERIAL PRIMARY KEY,
    session_id          TEXT NOT NULL UNIQUE,
    agent_id            TEXT NOT NULL,
    session_label       TEXT,
    provider            TEXT NOT NULL DEFAULT 'anthropic',
    model_id            TEXT NOT NULL DEFAULT 'unknown',
    input_tokens        BIGINT NOT NULL DEFAULT 0,
    output_tokens       BIGINT NOT NULL DEFAULT 0,
    cache_read_tokens   BIGINT NOT NULL DEFAULT 0,
    cache_write_tokens  BIGINT NOT NULL DEFAULT 0,
    total_tokens        BIGINT NOT NULL DEFAULT 0,
    input_cost_usd      NUMERIC(12,8) NOT NULL DEFAULT 0,
    output_cost_usd     NUMERIC(12,8) NOT NULL DEFAULT 0,
    cache_read_cost_usd NUMERIC(12,8) NOT NULL DEFAULT 0,
    cache_write_cost_usd NUMERIC(12,8) NOT NULL DEFAULT 0,
    total_cost_usd      NUMERIC(12,8) NOT NULL DEFAULT 0,
    request_count       INT NOT NULL DEFAULT 0,
    task_id             TEXT,
    duration_ms         BIGINT,
    exit_code           INT,
    files_changed       TEXT[],
    first_seen_at       TIMESTAMPTZ,
    last_seen_at        TIMESTAMPTZ,
    completed_at        TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_token_usage_agent_id ON token_usage(agent_id);
CREATE INDEX IF NOT EXISTS idx_token_usage_model_id ON token_usage(model_id);
CREATE INDEX IF NOT EXISTS idx_token_usage_first_seen_at ON token_usage(first_seen_at);
CREATE INDEX IF NOT EXISTS idx_token_usage_completed_at ON token_usage(completed_at);
CREATE INDEX IF NOT EXISTS idx_token_usage_task_id ON token_usage(task_id);
`

// EnsureSchema creates the token_usage table and indexes if they don't exist.
func EnsureSchema(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, schema)
	if err != nil {
		return fmt.Errorf("ensure schema: %w", err)
	}
	return nil
}
