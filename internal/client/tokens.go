package client

import (
	"context"
	"time"
)

// Token is a non-revoked personal access token as returned by the list endpoint.
// The raw token value is never included; use CreateTokenResponse to access it on creation.
type Token struct {
	ID         string     `json:"id"`
	UserID     string     `json:"user_id"`
	Name       string     `json:"name"`
	LastFour   string     `json:"last_four"`
	Status     string     `json:"status"`
	ExpiresAt  *time.Time `json:"expires_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	CreatedAt  time.Time  `json:"created_at"`
}

// CreateTokenResponse is returned by POST /v1/auth/tokens.
// The Token field holds the raw PAT value (sapi_<64hex>). This is the only time
// it is ever returned; the command layer must display it immediately.
type CreateTokenResponse struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Token     string     `json:"token"`
	LastFour  string     `json:"last_four"`
	Status    string     `json:"status"`
	ExpiresAt *time.Time `json:"expires_at"`
	CreatedAt time.Time  `json:"created_at"`
}

type createTokenRequest struct {
	Name      string     `json:"name"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// CreateToken creates a new personal access token for the authenticated user.
// The raw token in the response is shown exactly once; callers must surface it
// immediately with a warning that it cannot be retrieved again.
func (c *Client) CreateToken(ctx context.Context, name string, expiresAt *time.Time) (*CreateTokenResponse, error) {
	req := createTokenRequest{Name: name, ExpiresAt: expiresAt}
	var resp CreateTokenResponse
	if err := c.post(ctx, "/v1/auth/tokens", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// ListTokens returns all non-revoked personal access tokens owned by the authenticated user.
func (c *Client) ListTokens(ctx context.Context) ([]Token, error) {
	var resp []Token
	if err := c.get(ctx, "/v1/auth/tokens", &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

// RevokeToken revokes the personal access token identified by tokenID.
// The operation is idempotent; revoking an already-revoked token returns nil.
func (c *Client) RevokeToken(ctx context.Context, tokenID string) error {
	return c.delete(ctx, "/v1/auth/tokens/"+tokenID)
}
