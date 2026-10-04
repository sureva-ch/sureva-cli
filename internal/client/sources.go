package client

import (
	"context"
	"time"
)

// AppSource is one uploaded release of an upload-backed app. Fields mirror the
// cloud-api AppSource view (models.AppSource plus the computed available key),
// minus the storage internals (upload key, S3 key and version) the CLI does not surface.
type AppSource struct {
	ID    string `json:"id"`
	AppID string `json:"app_id"`
	// Seq is per-app and monotonic; the release tag is "src-<seq>".
	Seq int `json:"seq"`
	// Status is one of pending|validating|rejected|ready|expired.
	Status         string  `json:"status"`
	ReleaseTag     *string `json:"release_tag,omitempty"`
	SizeBytes      *int64  `json:"size_bytes,omitempty"`
	SHA256         *string `json:"sha256,omitempty"`
	StrippedPrefix *string `json:"stripped_prefix,omitempty"`
	// ValidationError is why a rejected archive was refused.
	ValidationError *string `json:"validation_error,omitempty"`
	// Available reports whether the stored version can still be deployed. It is
	// only present for rows that were promoted.
	Available *bool     `json:"available,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func sourcesPath(orgID, appID string) string {
	return "/v1/orgs/" + orgID + "/apps/" + appID + "/sources"
}

// ListSources returns the source releases of an upload-backed app.
func (c *Client) ListSources(ctx context.Context, orgID, appID string) ([]AppSource, error) {
	var resp []AppSource
	if err := c.get(ctx, sourcesPath(orgID, appID), &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

// GetSource returns one source release of an upload-backed app.
func (c *Client) GetSource(ctx context.Context, orgID, appID, sourceID string) (*AppSource, error) {
	var resp AppSource
	if err := c.get(ctx, sourcesPath(orgID, appID)+"/"+sourceID, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}
