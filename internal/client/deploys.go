package client

import (
	"context"
	"time"
)

// Deployment represents one deployment run for an application environment.
type Deployment struct {
	ID            string    `json:"id"`
	AppID         string    `json:"app_id"`
	EnvironmentID string    `json:"environment_id"`
	ReleaseTag    string    `json:"release_tag"`
	Status        string    `json:"status"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type triggerDeploymentRequest struct {
	ReleaseTag    string `json:"release_tag,omitempty"`
	EnvironmentID string `json:"environment_id,omitempty"`
}

// ListDeployments returns the last 50 deployments for the given app, newest first.
func (c *Client) ListDeployments(ctx context.Context, orgID, appID string) ([]Deployment, error) {
	var resp []Deployment
	if err := c.get(ctx, "/v1/orgs/"+orgID+"/apps/"+appID+"/deployments", &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

// TriggerDeployment starts a new deployment asynchronously and returns the pending
// deployment object. releaseTag is required for api and sse app types. Pass an empty
// environmentID to use the production environment.
func (c *Client) TriggerDeployment(ctx context.Context, orgID, appID, releaseTag, environmentID string) (*Deployment, error) {
	req := triggerDeploymentRequest{
		ReleaseTag:    releaseTag,
		EnvironmentID: environmentID,
	}
	var resp Deployment
	if err := c.post(ctx, "/v1/orgs/"+orgID+"/apps/"+appID+"/deployments", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// GetDeployment returns the deployment identified by deployID.
func (c *Client) GetDeployment(ctx context.Context, orgID, appID, deployID string) (*Deployment, error) {
	var resp Deployment
	if err := c.get(ctx, "/v1/orgs/"+orgID+"/apps/"+appID+"/deployments/"+deployID, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// CancelDeployment marks the deployment identified by deployID as cancelled.
func (c *Client) CancelDeployment(ctx context.Context, orgID, appID, deployID string) error {
	return c.delete(ctx, "/v1/orgs/"+orgID+"/apps/"+appID+"/deployments/"+deployID)
}
