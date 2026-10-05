package client

import (
	"context"
	"errors"
	"net/url"
	"time"
)

// errEmptySourceID is returned for a blank source id, which would otherwise
// turn the request into a different endpoint (the list).
var errEmptySourceID = errors.New("source id must not be empty")

// sourcePath is the path of one source. The id is user-supplied, so it is
// escaped as a single path segment: "/" or "?" in it cannot reach another
// endpoint.
func sourcePath(orgID, appID, sourceID string) (string, error) {
	if sourceID == "" {
		return "", errEmptySourceID
	}
	return sourcesPath(orgID, appID) + "/" + url.PathEscape(sourceID), nil
}

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
	return "/v1/orgs/" + url.PathEscape(orgID) + "/apps/" + url.PathEscape(appID) + "/sources"
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
	p, err := sourcePath(orgID, appID, sourceID)
	if err != nil {
		return nil, err
	}
	var resp AppSource
	if err := c.get(ctx, p, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// SourceUpload is what POST .../sources answers with: the new (pending) source,
// the presigned S3 POST form that accepts exactly one archive for it, and the
// limits that apply to that archive.
type SourceUpload struct {
	SourceID string `json:"source_id"`
	Upload   struct {
		URL string `json:"url"`
		// Fields are opaque: they carry the signed policy and must be sent back
		// verbatim, before the file part.
		Fields map[string]string `json:"fields"`
	} `json:"upload"`
	ExpiresAt time.Time `json:"expires_at"`
	// MaxBytes is the ceiling S3 enforces on the archive.
	MaxBytes int64  `json:"max_bytes"`
	Key      string `json:"key"`
}

// SourceCompletion is the 202 answer of the complete call. Validation runs
// asynchronously, so Status is normally "validating"; "rejected" means the
// platform could not even start it.
type SourceCompletion struct {
	SourceID string `json:"source_id"`
	Status   string `json:"status"`
}

// CreateSourceUpload asks for an upload target for one new archive of an
// upload-backed app. It creates a pending source; nothing is deployable until
// the archive is uploaded, completed and validated.
func (c *Client) CreateSourceUpload(ctx context.Context, orgID, appID string) (*SourceUpload, error) {
	var resp SourceUpload
	if err := c.post(ctx, sourcesPath(orgID, appID), nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// CompleteSource tells the platform the archive has been uploaded, which starts
// its validation.
func (c *Client) CompleteSource(ctx context.Context, orgID, appID, sourceID string) (*SourceCompletion, error) {
	p, err := sourcePath(orgID, appID, sourceID)
	if err != nil {
		return nil, err
	}
	var resp SourceCompletion
	if err := c.post(ctx, p+"/complete", nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// LatestSource is the sourceID that selects the app's newest ready release.
const LatestSource = "latest"

// SourceDownload is what GET .../sources/{id}/download answers with: a
// short-lived presigned URL for one release archive and the facts needed to
// verify it. SizeBytes and SHA256 describe the object at URL.
type SourceDownload struct {
	// URL is a credential: it grants a read of the archive until ExpiresAt. It
	// must never be printed or logged.
	URL        string    `json:"url"`
	ExpiresAt  time.Time `json:"expires_at"`
	SourceID   string    `json:"source_id"`
	ReleaseTag string    `json:"release_tag"`
	SizeBytes  *int64    `json:"size_bytes,omitempty"`
	SHA256     *string   `json:"sha256,omitempty"`
}

// DownloadSource asks for a download target for one release of an upload-backed
// app; sourceID may be LatestSource. The archive itself is fetched from storage
// with DownloadSourceArchive.
func (c *Client) DownloadSource(ctx context.Context, orgID, appID, sourceID string) (*SourceDownload, error) {
	p, err := sourcePath(orgID, appID, sourceID)
	if err != nil {
		return nil, err
	}
	var resp SourceDownload
	if err := c.get(ctx, p+"/download", &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}
