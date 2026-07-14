package client

import (
	"context"
	"time"
)

// CreateAppRequest is the request body for POST /orgs/{orgID}/apps.
// Fields mirror the cloud-api handler struct field-for-field so contract drift
// surfaces immediately as a test failure.
type CreateAppRequest struct {
	Name           string  `json:"name"`
	TeamID         string  `json:"team_id"`
	Label          *string `json:"label,omitempty"`
	Type           string  `json:"type"`
	Runtime        *string `json:"runtime,omitempty"`
	Region         string  `json:"region"`
	GitHubBranch   *string `json:"github_branch,omitempty"`
	BuildCommand   *string `json:"build_command,omitempty"`
	BuildOutputDir *string `json:"build_output_dir,omitempty"`
}

// DeleteAppResponse is the response body for DELETE /orgs/{orgID}/apps/{appID}.
type DeleteAppResponse struct {
	Status string `json:"status"`
}

// CreateApp creates a new application in the given org.
// Returns the full App object on 201 Created.
func (c *Client) CreateApp(ctx context.Context, orgID string, req CreateAppRequest) (*App, error) {
	var resp App
	if err := c.post(ctx, "/v1/orgs/"+orgID+"/apps", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// DeleteApp initiates async teardown of the given app.
// Returns a DeleteAppResponse with status "deleting" or "dispatch_failed" on 202 Accepted.
func (c *Client) DeleteApp(ctx context.Context, orgID, appID string) (*DeleteAppResponse, error) {
	var resp DeleteAppResponse
	if err := c.deleteInto(ctx, "/v1/orgs/"+orgID+"/apps/"+appID, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// App represents a Sureva application.
type App struct {
	ID             string    `json:"id"`
	OrgID          string    `json:"org_id"`
	Name           string    `json:"name"`
	Label          string    `json:"label"`
	Type           string    `json:"type"`
	Runtime        *string   `json:"runtime"`
	AWSRegion      string    `json:"aws_region"`
	GitHubRepoFull string    `json:"github_repo_full"`
	GitHubBranch   string    `json:"github_branch"`
	Subdomain      string    `json:"subdomain"`
	DomainStatus   string    `json:"domain_status"`
	Visibility     string    `json:"visibility"`
	IsActive       bool      `json:"is_active"`
	CreatedAt      time.Time `json:"created_at"`
}

// ListAllApps returns all apps visible to the authenticated user across every org,
// using the flat GET /v1/apps route. Useful for resolving an app-id to its org.
func (c *Client) ListAllApps(ctx context.Context) ([]App, error) {
	var resp []App
	if err := c.get(ctx, "/v1/apps", &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

// ListApps returns apps in the given org that are visible to the authenticated user.
func (c *Client) ListApps(ctx context.Context, orgID string) ([]App, error) {
	var resp []App
	if err := c.get(ctx, "/v1/orgs/"+orgID+"/apps", &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

// GetApp returns the app identified by appID within the given org.
func (c *Client) GetApp(ctx context.Context, orgID, appID string) (*App, error) {
	var resp App
	if err := c.get(ctx, "/v1/orgs/"+orgID+"/apps/"+appID, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}
