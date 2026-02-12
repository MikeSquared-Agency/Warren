package alexandria

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"time"
)

// Briefing represents the response from Alexandria's briefing API.
type Briefing struct {
	AgentID   string          `json:"agent_id"`
	Since     time.Time       `json:"since"`
	Items     json.RawMessage `json:"items"`
	ItemCount int             `json:"item_count"`
	Summary   string          `json:"summary"`
}

// Config holds Alexandria client configuration.
type Config struct {
	Enabled bool          `yaml:"enabled"`
	URL     string        `yaml:"url"`
	Timeout time.Duration `yaml:"timeout"`
}

// DefaultConfig returns sensible defaults.
func DefaultConfig() Config {
	return Config{
		Enabled: true,
		URL:     "http://warren_alexandria:8500",
		Timeout: 5 * time.Second,
	}
}

// Client is a client for the Alexandria briefing API.
type Client struct {
	baseURL    string
	httpClient *http.Client
	logger     *slog.Logger
}

// NewClient creates a new Alexandria client.
func NewClient(cfg Config, logger *slog.Logger) *Client {
	// Allow env var override.
	baseURL := cfg.URL
	if envURL := os.Getenv("ALEXANDRIA_URL"); envURL != "" {
		baseURL = envURL
	}

	return &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: cfg.Timeout,
		},
		logger: logger.With("component", "alexandria"),
	}
}

// GetBriefing fetches a wake-up briefing for the given agent.
// Returns nil if Alexandria is unreachable or returns an error (non-fatal).
func (c *Client) GetBriefing(ctx context.Context, agentID string, since time.Time, maxItems int) (*Briefing, error) {
	if maxItems <= 0 {
		maxItems = 50
	}

	u, err := url.Parse(fmt.Sprintf("%s/api/v1/briefings/%s", c.baseURL, url.PathEscape(agentID)))
	if err != nil {
		return nil, fmt.Errorf("build URL: %w", err)
	}

	q := u.Query()
	if !since.IsZero() {
		q.Set("since", since.UTC().Format(time.RFC3339))
	}
	q.Set("max_items", fmt.Sprintf("%d", maxItems))
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		c.logger.Warn("alexandria unreachable", "agent", agentID, "error", err)
		return nil, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		c.logger.Warn("alexandria returned non-OK", "agent", agentID, "status", resp.StatusCode, "body", string(body))
		return nil, nil
	}

	var briefing Briefing
	if err := json.NewDecoder(resp.Body).Decode(&briefing); err != nil {
		c.logger.Warn("failed to decode briefing", "agent", agentID, "error", err)
		return nil, nil
	}

	c.logger.Info("briefing retrieved", "agent", agentID, "items", briefing.ItemCount, "summary_len", len(briefing.Summary))
	return &briefing, nil
}
