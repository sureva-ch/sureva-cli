package client

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/sureva-ch/sureva-cli/internal/version"
)

// uploadTimeout bounds one archive upload. It is far longer than the API
// client's timeout because the body is tens of megabytes on an ordinary link.
const uploadTimeout = 15 * time.Minute

// UploadError is a refusal by the storage endpoint of the presigned form (S3).
// S3Code is the <Code> of its XML error body, such as EntityTooLarge or
// AccessDenied, when it sent one.
type UploadError struct {
	HTTPStatus int
	S3Code     string
	Message    string
}

func (e *UploadError) Error() string {
	if e.S3Code != "" {
		return fmt.Sprintf("upload refused (%d %s): %s", e.HTTPStatus, e.S3Code, e.Message)
	}
	return fmt.Sprintf("upload refused (%d): %s", e.HTTPStatus, e.Message)
}

// UploadSourceArchive posts the zip at archivePath to the presigned form as
// multipart/form-data: every returned field first, in a stable order, then the
// file part last, because S3 ignores fields after the file. The archive goes
// straight to the form's URL and never through the API, and the API bearer
// token is not sent there.
//
// S3 rejects a chunked POST, so the body length is computed up front rather
// than streaming through a pipe.
func (c *Client) UploadSourceArchive(ctx context.Context, up *SourceUpload, archivePath string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open archive: %w", err)
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		return fmt.Errorf("stat archive: %w", err)
	}

	var head bytes.Buffer
	mw := multipart.NewWriter(&head)
	keys := make([]string, 0, len(up.Upload.Fields))
	for k := range up.Upload.Fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if err := mw.WriteField(k, up.Upload.Fields[k]); err != nil {
			return err
		}
	}
	// The part header is written with the writer so the boundary framing stays
	// correct; the file bytes follow it, then the closing boundary.
	if _, err := mw.CreatePart(textproto.MIMEHeader{
		"Content-Disposition": {`form-data; name="file"; filename="source.zip"`},
		"Content-Type":        {"application/zip"},
	}); err != nil {
		return err
	}
	tail := "\r\n--" + mw.Boundary() + "--\r\n"

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, up.Upload.URL,
		io.MultiReader(bytes.NewReader(head.Bytes()), f, strings.NewReader(tail)))
	if err != nil {
		return fmt.Errorf("build upload request: %w", err)
	}
	req.ContentLength = int64(head.Len()) + st.Size() + int64(len(tail))
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("User-Agent", "sureva-cli/"+version.Version)

	resp, err := (&http.Client{Timeout: uploadTimeout}).Do(req)
	if err != nil {
		return networkError(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}

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
	return &UploadError{HTTPStatus: resp.StatusCode, S3Code: s3.Code, Message: msg}
}
