package client

import (
	"context"
	"fmt"
)

// Environment is a deployable environment of an app as returned by
// GET /v1/orgs/{orgID}/apps/{appID}/environments.
type Environment struct {
	ID        string  `json:"id"`
	AppID     string  `json:"app_id"`
	Name      string  `json:"name"`
	Branch    string  `json:"branch"`
	IsDefault bool    `json:"is_default"`
	IsActive  bool    `json:"is_active"`
	Subdomain *string `json:"subdomain,omitempty"`
}

// ListEnvironments returns the app's environments.
func (c *Client) ListEnvironments(ctx context.Context, orgID, appID string) ([]Environment, error) {
	var resp []Environment
	path := fmt.Sprintf("/v1/orgs/%s/apps/%s/environments", orgID, appID)
	if err := c.get(ctx, path, &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

// DefaultEnvironment returns the app's default environment: the one flagged
// is_default, falling back to the first active environment.
func (c *Client) DefaultEnvironment(ctx context.Context, orgID, appID string) (*Environment, error) {
	envs, err := c.ListEnvironments(ctx, orgID, appID)
	if err != nil {
		return nil, err
	}
	for i := range envs {
		if envs[i].IsDefault {
			return &envs[i], nil
		}
	}
	for i := range envs {
		if envs[i].IsActive {
			return &envs[i], nil
		}
	}
	return nil, &APIError{
		HTTPStatus: 404,
		Code:       "not_found",
		Message:    "app has no environments",
	}
}
