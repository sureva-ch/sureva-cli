package client

import (
	"context"
	"fmt"
	"net/http"
)

// LogEvent is a single CloudWatch log entry from a Lambda environment.
type LogEvent struct {
	Timestamp int64  `json:"timestamp"` // Unix milliseconds
	Message   string `json:"message"`
}

// LogsResponse holds a snapshot of CloudWatch log events for a Lambda-backed environment.
type LogsResponse struct {
	FunctionName string     `json:"function_name"`
	LogGroup     string     `json:"log_group"`
	Events       []LogEvent `json:"events"`
	NextToken    *string    `json:"next_token,omitempty"`
}

// GetLogs fetches a recent snapshot of log events for the given app environment
// (non-streaming). Returns an empty LogsResponse when the environment's Lambda
// has not yet been provisioned (HTTP 204 No Content).
func (c *Client) GetLogs(ctx context.Context, orgID, appID, envID string) (*LogsResponse, error) {
	path := fmt.Sprintf("/v1/orgs/%s/apps/%s/environments/%s/logs", orgID, appID, envID)

	resp, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}

	// 204 means the Lambda function has not been provisioned yet. Return an
	// empty result rather than an error — the caller can inform the user.
	if resp.StatusCode == http.StatusNoContent {
		_ = resp.Body.Close()
		return &LogsResponse{}, nil
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, parseErrorResponse(resp)
	}

	var result LogsResponse
	if err := decodeJSON(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}
