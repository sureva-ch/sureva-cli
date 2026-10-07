package client

import (
	"context"
	"net/url"
)

// Release is one release of a GitHub-backed app's repository, in the item shape
// cloud-api passes through from GitHub (github.Release). PublishedAt stays a
// string: GitHub leaves it empty for a draft and the CLI does not interpret it.
type Release struct {
	ID          int64  `json:"id"`
	TagName     string `json:"tag_name"`
	Name        string `json:"name"`
	Body        string `json:"body"`
	Draft       bool   `json:"draft"`
	Prerelease  bool   `json:"prerelease"`
	PublishedAt string `json:"published_at"`
	HTMLURL     string `json:"html_url"`
	Author      struct {
		Login     string `json:"login"`
		AvatarURL string `json:"avatar_url"`
	} `json:"author"`
}

func releasesPath(orgID, appID string) string {
	return "/v1/orgs/" + url.PathEscape(orgID) + "/apps/" + url.PathEscape(appID) + "/releases"
}

// ListReleases returns the releases of a GitHub-backed app, as the API sends
// them (one page of at most 100, drafts included). For an upload-backed app the
// API answers 200 with its source releases, whose rows carry fields this type
// does not: the caller must check the app's source type first.
func (c *Client) ListReleases(ctx context.Context, orgID, appID string) ([]Release, error) {
	var resp []Release
	if err := c.get(ctx, releasesPath(orgID, appID), &resp); err != nil {
		return nil, err
	}
	return resp, nil
}
