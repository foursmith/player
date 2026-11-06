package library

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ReadResource applies per-resource authentication to bounded subtitle/artwork
// reads. It owns no player, protocol client, or persistent URL cache.
func ReadResource(ctx context.Context, resource Resource, limit int64) ([]byte, error) {
	if limit <= 0 {
		return nil, errors.New("resource size limit is required")
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, resource.URL, nil)
	if err != nil {
		return nil, err
	}
	request.Header = resource.Headers.Clone()
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("resource HTTP status %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err == nil && int64(len(body)) > limit {
		return nil, errors.New("resource exceeds size limit")
	}
	return body, err
}
