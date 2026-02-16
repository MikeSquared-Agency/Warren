package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresStore implements UsageStore backed by a pgxpool connection.
type PostgresStore struct {
	pool *pgxpool.Pool
}

// NewPostgresStore connects to the database and verifies connectivity.
func NewPostgresStore(ctx context.Context, databaseURL string) (*PostgresStore, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("connect to database: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return &PostgresStore{pool: pool}, nil
}

// Pool returns the underlying pgxpool for schema migrations.
func (s *PostgresStore) Pool() *pgxpool.Pool {
	return s.pool
}

// Close closes the connection pool.
func (s *PostgresStore) Close() {
	s.pool.Close()
}

// UpsertUsage inserts or updates token/cost fields for a session.
// On conflict, only token/cost/request fields are updated — enrichment fields are left untouched.
func (s *PostgresStore) UpsertUsage(ctx context.Context, usage *TokenUsage) error {
	const q = `
INSERT INTO token_usage (
    session_id, agent_id, session_label, provider, model_id,
    input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, total_tokens,
    input_cost_usd, output_cost_usd, cache_read_cost_usd, cache_write_cost_usd, total_cost_usd,
    request_count, first_seen_at, last_seen_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)
ON CONFLICT (session_id) DO UPDATE SET
    agent_id         = EXCLUDED.agent_id,
    session_label    = EXCLUDED.session_label,
    provider         = EXCLUDED.provider,
    model_id         = EXCLUDED.model_id,
    input_tokens     = EXCLUDED.input_tokens,
    output_tokens    = EXCLUDED.output_tokens,
    cache_read_tokens = EXCLUDED.cache_read_tokens,
    cache_write_tokens = EXCLUDED.cache_write_tokens,
    total_tokens     = EXCLUDED.total_tokens,
    input_cost_usd   = EXCLUDED.input_cost_usd,
    output_cost_usd  = EXCLUDED.output_cost_usd,
    cache_read_cost_usd = EXCLUDED.cache_read_cost_usd,
    cache_write_cost_usd = EXCLUDED.cache_write_cost_usd,
    total_cost_usd   = EXCLUDED.total_cost_usd,
    request_count    = EXCLUDED.request_count,
    first_seen_at    = LEAST(token_usage.first_seen_at, EXCLUDED.first_seen_at),
    last_seen_at     = GREATEST(token_usage.last_seen_at, EXCLUDED.last_seen_at),
    updated_at       = NOW()
`
	_, err := s.pool.Exec(ctx, q,
		usage.SessionID, usage.AgentID, usage.SessionLabel, usage.Provider, usage.ModelID,
		usage.InputTokens, usage.OutputTokens, usage.CacheReadTokens, usage.CacheWriteTokens, usage.TotalTokens,
		usage.InputCostUSD, usage.OutputCostUSD, usage.CacheReadCostUSD, usage.CacheWriteCostUSD, usage.TotalCostUSD,
		usage.RequestCount, usage.FirstSeenAt, usage.LastSeenAt,
	)
	if err != nil {
		return fmt.Errorf("upsert usage: %w", err)
	}
	return nil
}

// EnrichSession updates enrichment fields from CC sidecar completion events.
// Creates a stub row if the tailer hasn't processed this session yet.
func (s *PostgresStore) EnrichSession(ctx context.Context, sessionID, agentID, taskID string, durationMs int64, exitCode int, filesChanged []string) error {
	const q = `
INSERT INTO token_usage (session_id, agent_id, task_id, duration_ms, exit_code, files_changed, completed_at)
VALUES ($1, $2, $3, $4, $5, $6, NOW())
ON CONFLICT (session_id) DO UPDATE SET
    task_id       = COALESCE(EXCLUDED.task_id, token_usage.task_id),
    duration_ms   = EXCLUDED.duration_ms,
    exit_code     = EXCLUDED.exit_code,
    files_changed = EXCLUDED.files_changed,
    completed_at  = NOW(),
    updated_at    = NOW()
`
	_, err := s.pool.Exec(ctx, q, sessionID, agentID, taskID, durationMs, exitCode, filesChanged)
	if err != nil {
		return fmt.Errorf("enrich session: %w", err)
	}
	return nil
}

// GetSummary returns aggregate usage since the given time.
func (s *PostgresStore) GetSummary(ctx context.Context, since time.Time) (*UsageSummary, error) {
	summary := &UsageSummary{}

	// Totals.
	err := s.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(total_tokens),0), COALESCE(SUM(total_cost_usd),0),
		       COUNT(*), COALESCE(SUM(request_count),0)
		FROM token_usage WHERE first_seen_at >= $1 OR first_seen_at IS NULL
	`, since).Scan(&summary.TotalTokens, &summary.TotalCostUSD, &summary.TotalSessions, &summary.TotalRequests)
	if err != nil {
		return nil, fmt.Errorf("get summary totals: %w", err)
	}

	// By agent.
	rows, err := s.pool.Query(ctx, `
		SELECT agent_id, COALESCE(SUM(total_tokens),0), COALESCE(SUM(total_cost_usd),0),
		       COUNT(*), COALESCE(SUM(request_count),0)
		FROM token_usage WHERE first_seen_at >= $1 OR first_seen_at IS NULL
		GROUP BY agent_id ORDER BY SUM(total_cost_usd) DESC
	`, since)
	if err != nil {
		return nil, fmt.Errorf("get summary by agent: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var a AgentUsageSummary
		if err := rows.Scan(&a.AgentID, &a.TotalTokens, &a.TotalCostUSD, &a.SessionCount, &a.RequestCount); err != nil {
			return nil, fmt.Errorf("scan agent summary: %w", err)
		}
		summary.ByAgent = append(summary.ByAgent, a)
	}

	// By model.
	rows, err = s.pool.Query(ctx, `
		SELECT model_id, COALESCE(SUM(total_tokens),0), COALESCE(SUM(total_cost_usd),0),
		       COUNT(*), COALESCE(SUM(request_count),0)
		FROM token_usage WHERE first_seen_at >= $1 OR first_seen_at IS NULL
		GROUP BY model_id ORDER BY SUM(total_cost_usd) DESC
	`, since)
	if err != nil {
		return nil, fmt.Errorf("get summary by model: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var m ModelUsageSummary
		if err := rows.Scan(&m.ModelID, &m.TotalTokens, &m.TotalCostUSD, &m.SessionCount, &m.RequestCount); err != nil {
			return nil, fmt.Errorf("scan model summary: %w", err)
		}
		summary.ByModel = append(summary.ByModel, m)
	}

	return summary, nil
}

// GetAgentUsage returns usage for a specific agent since the given time.
func (s *PostgresStore) GetAgentUsage(ctx context.Context, agentID string, since time.Time) (*AgentUsageSummary, error) {
	a := &AgentUsageSummary{AgentID: agentID}
	err := s.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(total_tokens),0), COALESCE(SUM(total_cost_usd),0),
		       COUNT(*), COALESCE(SUM(request_count),0)
		FROM token_usage WHERE agent_id = $1 AND (first_seen_at >= $2 OR first_seen_at IS NULL)
	`, agentID, since).Scan(&a.TotalTokens, &a.TotalCostUSD, &a.SessionCount, &a.RequestCount)
	if err != nil {
		return nil, fmt.Errorf("get agent usage: %w", err)
	}
	return a, nil
}

// GetModelUsage returns usage for a specific model since the given time.
func (s *PostgresStore) GetModelUsage(ctx context.Context, modelID string, since time.Time) (*ModelUsageSummary, error) {
	m := &ModelUsageSummary{ModelID: modelID}
	err := s.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(total_tokens),0), COALESCE(SUM(total_cost_usd),0),
		       COUNT(*), COALESCE(SUM(request_count),0)
		FROM token_usage WHERE model_id = $1 AND (first_seen_at >= $2 OR first_seen_at IS NULL)
	`, modelID, since).Scan(&m.TotalTokens, &m.TotalCostUSD, &m.SessionCount, &m.RequestCount)
	if err != nil {
		return nil, fmt.Errorf("get model usage: %w", err)
	}
	return m, nil
}

// GetCostEfficiency returns cost efficiency metrics for Dispatch routing.
func (s *PostgresStore) GetCostEfficiency(ctx context.Context, agentID string) (*CostEfficiency, error) {
	ce := &CostEfficiency{AgentID: agentID}
	err := s.pool.QueryRow(ctx, `
		SELECT
			COALESCE(AVG(total_cost_usd), 0),
			COALESCE(AVG(total_tokens), 0),
			COALESCE(AVG(duration_ms), 0),
			COALESCE(
				SUM(CASE WHEN exit_code = 0 THEN 1 ELSE 0 END)::FLOAT /
				NULLIF(COUNT(CASE WHEN exit_code IS NOT NULL THEN 1 END), 0),
				0
			),
			COUNT(*)
		FROM token_usage
		WHERE agent_id = $1
	`, agentID).Scan(&ce.AvgCostUSD, &ce.AvgTokens, &ce.AvgDurationMs, &ce.SuccessRate, &ce.SessionCount)
	if err != nil {
		return nil, fmt.Errorf("get cost efficiency: %w", err)
	}
	return ce, nil
}
