package client

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

// endlessBody never ends; reading more than the limit fails the test instead of
// hanging it.
type endlessBody struct {
	t    *testing.T
	read int
}

func (b *endlessBody) Read(p []byte) (int, error) {
	if b.read > 8*maxErrorBodyBytes {
		b.t.Error("the error body was read without a bound")
		return 0, errors.New("unbounded read")
	}
	for i := range p {
		p[i] = 'x'
	}
	b.read += len(p)
	return len(p), nil
}

func (b *endlessBody) Close() error { return nil }

func TestParseErrorResponse_BoundsTheBodyItReads(t *testing.T) {
	t.Parallel()
	body := &endlessBody{t: t}
	resp := &http.Response{StatusCode: http.StatusConflict, Body: body}

	apiErr := parseErrorResponse(resp)

	if body.read > maxErrorBodyBytes+64*1024 {
		t.Errorf("read %d bytes, limit is %d", body.read, maxErrorBodyBytes)
	}
	if apiErr.HTTPStatus != http.StatusConflict || apiErr.Message != http.StatusText(http.StatusConflict) {
		t.Errorf("an unparseable body falls back to the status text: %+v", apiErr)
	}
}

func TestParseErrorResponse_ReadsAnEnvelopeWithinTheLimit(t *testing.T) {
	t.Parallel()
	resp := &http.Response{StatusCode: http.StatusConflict,
		Body: io.NopCloser(strings.NewReader(`{"error":"msg","code":"source_retry_too_soon","retry_after_seconds":7}`))}
	if apiErr := parseErrorResponse(resp); apiErr.Message != "msg" || apiErr.RetryAfterSeconds != 7 {
		t.Errorf("apiErr = %+v", apiErr)
	}
}
