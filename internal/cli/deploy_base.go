package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sureva-ch/sureva-cli/internal/client"
	"github.com/sureva-ch/sureva-cli/internal/sourcebase"
)

// unusableBaseMessage explains a request-time refusal of the base that was sent
// (400 invalid_base_source_id, 422 base_source_not_found): the record in
// .sureva/source.json does not name a release of this app. The record is left
// alone; the caller decides whether to replace it.
func unusableBaseMessage(base *sourcebase.Base, apiMessage string) string {
	return fmt.Sprintf("the release recorded in %s (%s) cannot be used as the base of this upload: %s. "+
		"Nothing was uploaded. Pull the app's current release into a new directory with 'sureva sources pull', "+
		"or pass --no-base to upload without a base and overwrite the latest release on purpose",
		sourcebase.Dir+"/"+sourcebase.File, base.SourceID, apiMessage)
}

// staleBaseFailure builds the message and details of a stale_base rejection: the
// release this directory was based on is no longer the app's latest ready one,
// so the platform did not publish the archive. It names the release that is
// latest by listing the app's sources (structured data) rather than reading the
// API's message; when the list cannot be read the message says so without it.
func staleBaseFailure(ctx context.Context, c *client.Client, orgID, appID string, base *sourcebase.Base, rejected *client.AppSource) (string, map[string]any) {
	details := rejectionDetails(rejected)
	was := "the release this directory was based on"
	if base != nil && base.ReleaseTag != "" {
		was = fmt.Sprintf("%s (%s), the release this directory was based on,", base.ReleaseTag, base.SourceID)
	} else if base != nil {
		was = fmt.Sprintf("%s, the release this directory was based on,", base.SourceID)
	}

	latest := "a newer release"
	if sources, err := c.ListSources(ctx, orgID, appID); err == nil {
		if s := latestReady(sources); s != nil {
			details["latest_source_id"] = s.ID
			if s.ReleaseTag != nil {
				latest = *s.ReleaseTag
				details["latest_release_tag"] = *s.ReleaseTag
			}
		}
	}
	return fmt.Sprintf("%s is no longer the latest: %s exists, so nothing was published. "+
		"Pull the latest release into a separate directory ('sureva sources pull %s --dir <new-dir>'), reapply your change there and run deploy again from it; "+
		"or run deploy again with --no-base to overwrite %s on purpose",
		was, latest, appID, latest), details
}

// latestReady is the app's latest release as the API means it: the ready source
// with the highest seq. The list is ordered by creation time, which is not the
// same thing (a release created earlier can be promoted later), so its order is
// not used. nil when no source is ready.
func latestReady(sources []client.AppSource) *client.AppSource {
	var latest *client.AppSource
	for i := range sources {
		if s := &sources[i]; s.Status == "ready" && (latest == nil || s.Seq > latest.Seq) {
			latest = s
		}
	}
	return latest
}

// isUUID reports whether s is a UUID in one of the forms the API accepts for a
// source id: the canonical 8-4-4-4-12 hexadecimal groups, 32 hexadecimal digits
// without hyphens, either of them in braces or after "urn:uuid:".
func isUUID(s string) bool {
	s = strings.TrimPrefix(s, "urn:uuid:")
	if len(s) == 38 && s[0] == '{' && s[37] == '}' {
		s = s[1:37]
	}
	hyphens := false
	switch len(s) {
	case 36:
		hyphens = true
	case 32:
	default:
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if hyphens && (i == 8 || i == 13 || i == 18 || i == 23) {
			if c != '-' {
				return false
			}
			continue
		}
		if !isHexDigit(c) {
			return false
		}
	}
	return true
}

func isHexDigit(c byte) bool {
	switch {
	case c >= '0' && c <= '9', c >= 'a' && c <= 'f', c >= 'A' && c <= 'F':
		return true
	}
	return false
}

// recordPublished moves dir's .sureva/source.json to the release that was just
// published, so the next deploy from this directory is based on it (and does
// not come out stale against its own previous upload). It also creates the
// record for a directory that was never pulled: that directory had no base for
// its first upload, but after it publishes it has one.
//
// SHA256 is the checksum the API reports for the stored release, the one a pull
// of it would record: the stored archive is the platform's rewrite of the
// upload, so its checksum differs from the zip that was sent.
//
// A failure never fails the deploy, which already succeeded as far as the
// release goes; it is reported on res.
func recordPublished(dir, appID string, source *client.AppSource, res *deployResult) {
	b := sourcebase.Base{AppID: appID, SourceID: source.ID, PulledAt: time.Now().UTC()}
	if source.ReleaseTag != nil {
		b.ReleaseTag = *source.ReleaseTag
	}
	if source.SHA256 != nil {
		b.SHA256 = *source.SHA256
	}
	prev := sourcebase.Read(dir)
	p, err := sourcebase.Write(dir, b)
	if err != nil {
		res.StateFileError = fmt.Sprintf("the release was published but %s could not be updated: %v; the next deploy from this directory will not be based on this release (run 'sureva sources pull' into a fresh directory, or deploy with --no-base)", sourcebase.Path(dir), err)
		return
	}
	res.StateFile = p
	if prev != nil && prev.AppID != appID {
		res.StateFileReplaced = &replacedRecord{AppID: prev.AppID, SourceID: prev.SourceID, ReleaseTag: prev.ReleaseTag}
	}
}
