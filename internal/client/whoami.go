package client

import "context"

// AuthUser is the public caller profile returned by GET /v1/auth/me.
type AuthUser struct {
	ID        string `json:"id"`
	Email     string `json:"email"`
	Name      string `json:"name"`
	IsActive  bool   `json:"is_active"`
	CreatedAt string `json:"created_at"`
}

// Whoami returns the identity of the caller authenticated by the current token.
func (c *Client) Whoami(ctx context.Context) (*AuthUser, error) {
	var resp AuthUser
	if err := c.get(ctx, "/v1/auth/me", &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}
