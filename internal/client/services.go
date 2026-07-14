package client

import (
	"context"
	"net/url"
	"time"
)

// KVSTable is a named managed KVS table namespace scoped to an app.
type KVSTable struct {
	ID            string     `json:"id"`
	OrgID         string     `json:"org_id,omitempty"`
	AppID         string     `json:"app_id,omitempty"`
	EnvID         string     `json:"env_id,omitempty"`
	Name          string     `json:"name"`
	Status        string     `json:"status"`
	KVSURL        string     `json:"kvs_url"`
	EnvVarName    string     `json:"env_var_name,omitempty"`
	TokenLastFour *string    `json:"token_last_four,omitempty"`
	MinuteLimit   int        `json:"minute_limit"`
	DisabledAt    *time.Time `json:"disabled_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at,omitempty"`
	UpdatedAt     time.Time  `json:"updated_at,omitempty"`
}

// CreateKVSTableRequest is the request body for POST /services/kvs/tables.
type CreateKVSTableRequest struct {
	Name        string `json:"name"`
	MinuteLimit int    `json:"minute_limit,omitempty"`
}

// KVSTableResponse is returned by table create and rotate operations.
type KVSTableResponse struct {
	Table *KVSTable `json:"table"`
	Token string    `json:"token"`
}

func kvsPath(orgID, appID string) string {
	return "/v1/orgs/" + orgID + "/apps/" + appID + "/services/kvs"
}

func kvsTablesPath(orgID, appID string) string {
	return kvsPath(orgID, appID) + "/tables"
}

// ListKVSTables returns named KVS table namespaces for an app.
func (c *Client) ListKVSTables(ctx context.Context, orgID, appID string) ([]KVSTable, error) {
	var resp []KVSTable
	if err := c.get(ctx, kvsTablesPath(orgID, appID), &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

// CreateKVSTable creates a named KVS table namespace and returns a one-time token.
func (c *Client) CreateKVSTable(ctx context.Context, orgID, appID string, req CreateKVSTableRequest) (*KVSTableResponse, error) {
	var resp KVSTableResponse
	if err := c.post(ctx, kvsTablesPath(orgID, appID), req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// DeleteKVSTable disables a named KVS table namespace.
func (c *Client) DeleteKVSTable(ctx context.Context, orgID, appID, name string) error {
	return c.delete(ctx, kvsTablesPath(orgID, appID)+"/"+url.PathEscape(name))
}

// RotateKVSTableToken rotates a named table token and returns the new one-time token.
func (c *Client) RotateKVSTableToken(ctx context.Context, orgID, appID, name string) (*KVSTableResponse, error) {
	var resp KVSTableResponse
	if err := c.post(ctx, kvsTablesPath(orgID, appID)+"/"+url.PathEscape(name)+"/rotate", nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}
