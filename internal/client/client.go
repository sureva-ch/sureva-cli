package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"strings"
	"time"

	"github.com/sureva-ch/sureva-cli/internal/version"
)

const (
	defaultTimeout = 30 * time.Second
	maxRetries     = 3
)

// Option is a functional option applied to a Client on construction.
type Option func(*Client)

// WithHTTPClient replaces the underlying http.Client. Intended for testing.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) { c.httpClient = hc }
}

// WithRetryDelay overrides the retry delay function. Pass func(int) time.Duration { return 0 }
// in tests to skip sleeping between attempts.
func WithRetryDelay(fn func(int) time.Duration) Option {
	return func(c *Client) { c.retryDelay = fn }
}

// Client is a hand-rolled HTTP client for the cloud-api. Each method corresponds
// to one API route. The client injects Authorization, Content-Type, and User-Agent
// headers on every request and retries transient 5xx and network failures.
type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
	retryDelay func(int) time.Duration
}

// New creates a Client targeting baseURL using token as the bearer credential.
// baseURL is https://api.sureva.com by default. A trailing slash or accidental
// /v1 suffix is stripped because every client route owns the /v1 prefix.
func New(baseURL, token string, opts ...Option) *Client {
	baseURL = strings.TrimRight(baseURL, "/")
	baseURL = strings.TrimSuffix(baseURL, "/v1")
	c := &Client{
		baseURL: baseURL,
		token:   token,
		httpClient: &http.Client{
			Timeout: defaultTimeout,
		},
		retryDelay: defaultRetryDelay,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// defaultRetryDelay returns exponential backoff with jitter for the nth retry (1-indexed).
// attempt=1 → ~500ms base; attempt=2 → ~1s base; capped at 5s.
func defaultRetryDelay(attempt int) time.Duration {
	const maxDelay = 5 * time.Second
	base := 500 * time.Millisecond * (1 << (attempt - 1))
	if base > maxDelay {
		base = maxDelay
	}
	jitter := time.Duration(rand.Int64N(int64(base / 2)))
	return base + jitter
}

// get performs a GET request and decodes the 2xx response body into v.
func (c *Client) get(ctx context.Context, path string, v any) error {
	resp, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return parseErrorResponse(resp)
	}
	return decodeJSON(resp, v)
}

// post performs a POST request with body and decodes the 2xx response into v.
func (c *Client) post(ctx context.Context, path string, body, v any) error {
	resp, err := c.do(ctx, http.MethodPost, path, body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return parseErrorResponse(resp)
	}
	return decodeJSON(resp, v)
}

// put performs a PUT request with body. Expects 2xx (typically 204); no body decoded.
func (c *Client) put(ctx context.Context, path string, body any) error {
	resp, err := c.do(ctx, http.MethodPut, path, body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return parseErrorResponse(resp)
	}
	_ = resp.Body.Close()
	return nil
}

// delete performs a DELETE request. Expects 2xx (typically 204); no body decoded.
func (c *Client) delete(ctx context.Context, path string) error {
	resp, err := c.do(ctx, http.MethodDelete, path, nil)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return parseErrorResponse(resp)
	}
	_ = resp.Body.Close()
	return nil
}

// deleteInto performs a DELETE request and decodes the 2xx response body into v.
// Unlike delete(), this variant surfaces the response body — used for endpoints
// that return a status payload on 202 (e.g. DELETE /apps/{id} → {"status":"deleting"}).
// DELETE remains idempotent per isIdempotent; a retried teardown-dispatch is safe.
func (c *Client) deleteInto(ctx context.Context, path string, v any) error {
	resp, err := c.do(ctx, http.MethodDelete, path, nil)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return parseErrorResponse(resp)
	}
	return decodeJSON(resp, v)
}

// isIdempotent reports whether a method is safe to retry. POST is excluded:
// a 5xx or a network failure after the request was sent may have already
// produced the side effect (a deployment, a token), and retrying would
// duplicate it.
func isIdempotent(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPut, http.MethodDelete:
		return true
	}
	return false
}

// do executes the request with retry logic.
//
// Retry policy:
//   - Only idempotent methods (GET/HEAD/PUT/DELETE) are ever retried.
//     POST gets exactly one attempt.
//   - 5xx responses: retry up to maxRetries times with the configured delay.
//   - Network errors: retry up to maxRetries times.
//   - 4xx responses: returned immediately; never retried.
//   - Context cancellation during a retry delay: propagated as a network error.
//
// The caller receives either (resp, nil) or (nil, *APIError). A non-nil resp
// always has an open body; the caller is responsible for closing it.
func (c *Client) do(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var (
		lastNetErr       error
		lastServerStatus int
	)

	attempts := maxRetries
	if !isIdempotent(method) {
		attempts = 1
	}

	for attempt := range attempts {
		if attempt > 0 {
			delay := c.retryDelay(attempt)
			select {
			case <-ctx.Done():
				return nil, networkError(ctx.Err())
			case <-time.After(delay):
			}
		}

		resp, err := c.doOnce(ctx, method, path, body)
		if err != nil {
			lastNetErr = err
			lastServerStatus = 0
			continue
		}

		// 4xx: surface immediately without retry.
		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			return resp, nil
		}

		// 5xx: close body and retry if attempts remain.
		if resp.StatusCode >= 500 {
			lastServerStatus = resp.StatusCode
			lastNetErr = nil
			_ = resp.Body.Close()
			continue
		}

		// 2xx/3xx: success.
		return resp, nil
	}

	// All retries exhausted.
	if lastServerStatus != 0 {
		return nil, &APIError{
			HTTPStatus: lastServerStatus,
			Code:       "server_error",
			Message:    fmt.Sprintf("server error after %d attempts", attempts),
		}
	}
	return nil, networkError(lastNetErr)
}

// doOnce performs a single HTTP request, injecting auth and standard headers.
// The path must start with "/" (e.g. "/v1/orgs"). The base URL may or may
// not have a trailing slash; joining is always double-slash-free.
func (c *Client) doOnce(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	url := c.baseURL + "/" + strings.TrimLeft(path, "/")
	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("User-Agent", "sureva-cli/"+version.Version)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	return c.httpClient.Do(req)
}

// decodeJSON reads and closes the response body, decoding it into v.
func decodeJSON(resp *http.Response, v any) error {
	decodeErr := json.NewDecoder(resp.Body).Decode(v)
	closeErr := resp.Body.Close()
	if decodeErr != nil {
		return fmt.Errorf("decode response: %w", decodeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close response body: %w", closeErr)
	}
	return nil
}
