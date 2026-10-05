package cli

import (
	"context"
	"errors"
	"time"

	"github.com/sureva-ch/sureva-cli/internal/client"
)

// maxCompleteRetries is how often 'complete' is repeated after validation was
// refused for a reason on the platform's side (a rejection the API marks
// retryable). The API restarts validation of such a row and answers 202 again.
// Three repeats are enough to ride out a transient storage or dispatch failure
// without hiding a lasting outage behind a long wait.
const maxCompleteRetries = 3

// maxCompleteBackoff caps the pause before a repeated 'complete'.
const maxCompleteBackoff = time.Minute

// completeBackoff is the pause before the nth repeat (1-indexed): the wait
// interval, doubled each time. Deriving it from --wait-interval keeps the pacing
// in one flag and lets tests run without a clock.
func completeBackoff(interval time.Duration, retry int) time.Duration {
	d := interval << retry
	if d <= 0 || d > maxCompleteBackoff {
		return maxCompleteBackoff
	}
	return d
}

// awaitValidation polls the source until validation reaches a verdict. A
// rejection the API marks retryable is not final: the archive was never judged,
// so 'complete' is repeated, up to maxCompleteRetries times, pausing a little
// longer each time. A rejection that is not retryable is returned at once.
//
// timeout bounds the whole call, retries and pauses included. It returns the
// last source read, how many times 'complete' was repeated, and errWaitTimeout
// when the bound was reached; any other error is the API's or the context's.
func awaitValidation(ctx context.Context, c *client.Client, orgID, appID, sourceID string, interval, timeout time.Duration) (*client.AppSource, int, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	// A deadline that ends a request or a pause is the wait timeout, not an
	// API failure.
	timedOut := func(err error) error {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return errWaitTimeout
		}
		return err
	}

	var source *client.AppSource
	for retries := 0; ; {
		err := pollUntil(ctx, interval, timeout, func(ctx context.Context) (bool, error) {
			s, gErr := c.GetSource(ctx, orgID, appID, sourceID)
			if gErr != nil {
				return false, gErr
			}
			source = s
			switch s.Status {
			case "ready", "rejected", "expired":
				return true, nil
			}
			return false, nil
		})
		if err != nil {
			return source, retries, timedOut(err)
		}
		if !source.IsRetryable() || retries >= maxCompleteRetries {
			return source, retries, nil
		}

		retries++
		select {
		case <-ctx.Done():
			return source, retries - 1, timedOut(ctx.Err())
		case <-time.After(completeBackoff(interval, retries)):
		}
		if _, err := c.CompleteSource(ctx, orgID, appID, sourceID); err != nil {
			return source, retries - 1, timedOut(err)
		}
	}
}

// rejectionDetails is what a refused source adds to the error envelope.
func rejectionDetails(s *client.AppSource) map[string]any {
	d := map[string]any{"source_status": s.Status}
	if s.ValidationCode != nil && *s.ValidationCode != "" {
		d["validation_code"] = *s.ValidationCode
	}
	if s.Retryable != nil {
		d["retryable"] = *s.Retryable
	}
	return d
}
