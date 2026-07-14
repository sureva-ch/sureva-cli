package client

import (
	"context"
	"time"
)

// Team represents a Sureva team within an organization.
type Team struct {
	ID        string    `json:"id"`
	OrgID     string    `json:"org_id"`
	Slug      string    `json:"slug"`
	Name      string    `json:"name"`
	IsSystem  bool      `json:"is_system"`
	IsActive  bool      `json:"is_active"`
	CreatedAt time.Time `json:"created_at"`
}

// ListTeams returns all teams in the given org.
func (c *Client) ListTeams(ctx context.Context, orgID string) ([]Team, error) {
	var resp []Team
	if err := c.get(ctx, "/v1/orgs/"+orgID+"/teams", &resp); err != nil {
		return nil, err
	}
	return resp, nil
}
