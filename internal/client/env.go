package client

import "context"

// GetEnv returns the app-level environment variables as a key/value map.
// Values are returned as stored; masking (***) is the command layer's responsibility.
// Requires effective app maintainer role.
func (c *Client) GetEnv(ctx context.Context, orgID, appID string) (map[string]string, error) {
	var resp map[string]string
	if err := c.get(ctx, "/v1/orgs/"+orgID+"/apps/"+appID+"/env", &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

// SetEnv replaces the app-level environment variables with the provided map.
// This is a full replacement (PUT semantics); omitted keys are deleted.
// Requires effective app maintainer role.
func (c *Client) SetEnv(ctx context.Context, orgID, appID string, vars map[string]string) error {
	return c.put(ctx, "/v1/orgs/"+orgID+"/apps/"+appID+"/env", vars)
}
