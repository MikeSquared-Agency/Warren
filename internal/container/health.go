package container

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

var healthClient = &http.Client{
	Timeout: 5 * time.Second,
}

func CheckHealth(ctx context.Context, url string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("create health request: %w", err)
	}

	resp, err := healthClient.Do(req)
	if err != nil {
		return fmt.Errorf("health check failed: %w", err)
	}
	resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return fmt.Errorf("health check returned status %d", resp.StatusCode)
	}

	return nil
}
