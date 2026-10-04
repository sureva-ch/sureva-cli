package client

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/sureva-ch/sureva-cli/internal/version"
)

// maxRedirects bounds the redirects followed from a download URL.
const maxRedirects = 5

// ErrDownloadTooLarge means the storage endpoint sent more bytes than the API
// said the archive holds.
var ErrDownloadTooLarge = errors.New("the download is larger than the size the API reported")

// DownloadError is a refusal by the storage endpoint of the presigned URL (S3).
// S3Code is the <Code> of its XML error body, such as AccessDenied or
// ExpiredToken, when it sent one.
type DownloadError struct {
	HTTPStatus int
	S3Code     string
	Message    string
}

func (e *DownloadError) Error() string {
	if e.S3Code != "" {
		return fmt.Sprintf("download refused (%d %s): %s", e.HTTPStatus, e.S3Code, e.Message)
	}
	return fmt.Sprintf("download refused (%d): %s", e.HTTPStatus, e.Message)
}

// Expired reports whether storage refused the link because it ran out, which
// the caller fixes by asking the API for a new one.
func (e *DownloadError) Expired() bool {
	return e.S3Code == "ExpiredToken" || strings.Contains(strings.ToLower(e.Message), "expired")
}

// DownloadSourceArchive fetches the presigned url straight from storage into
// dst and returns the hex sha256 and the number of bytes written. The API bearer
// token is not sent there: the request is built here, on a fresh http.Client,
// with no Authorization header, and none is forwarded on a redirect either.
//
// At most maxBytes are read; one more byte is ErrDownloadTooLarge. The body is
// streamed through the hash into dst and never held in memory. An error never
// carries the URL, which is a credential.
func (c *Client) DownloadSourceArchive(ctx context.Context, rawURL string, maxBytes int64, dst io.Writer) (string, int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", 0, errors.New("the download URL is not valid")
	}
	req.Header.Set("User-Agent", "sureva-cli/"+version.Version)

	hc := &http.Client{
		Timeout: uploadTimeout,
		CheckRedirect: func(r *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return errors.New("too many redirects")
			}
			r.Header.Del("Authorization")
			return nil
		},
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "", 0, downloadTransportError(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		var s3 struct {
			Code    string `xml:"Code"`
			Message string `xml:"Message"`
		}
		_ = xml.Unmarshal(body, &s3)
		msg := s3.Message
		if msg == "" {
			msg = http.StatusText(resp.StatusCode)
		}
		return "", 0, &DownloadError{HTTPStatus: resp.StatusCode, S3Code: s3.Code, Message: msg}
	}
	if resp.ContentLength > maxBytes {
		return "", 0, ErrDownloadTooLarge
	}

	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(dst, h), io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		if ctx.Err() != nil {
			return "", n, ctx.Err()
		}
		return "", n, downloadTransportError(err)
	}
	if n > maxBytes {
		return "", n, ErrDownloadTooLarge
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// downloadTransportError reports a failure with no HTTP answer as a network
// error. net/http wraps these in *url.Error, whose text includes the full URL
// with its signature, so only the cause is kept.
func downloadTransportError(err error) *APIError {
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	return networkError(err)
}
