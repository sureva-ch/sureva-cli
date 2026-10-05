package cli

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/sureva-ch/sureva-cli/internal/client"
)

// maxCompleteRetries is how often 'complete' is repeated after validation was
// refused for a reason on the platform's side (a rejection the API marks
// retryable) when the API reports no attempt limit of its own. The API restarts
// validation of such a row and answers 202 again. Three repeats are enough to
// ride out a transient storage or dispatch failure without hiding a lasting
// outage behind a long wait.
const maxCompleteRetries = 3

// maxLimitedRetries is the safety net for an API that reports its attempt
// limit: the row's `retryable` decides whether another 'complete' is worth
// sending, and this only stops an API that keeps saying yes.
const maxLimitedRetries = 10

// maxCompleteBackoff caps the pause before a repeated 'complete'.
const maxCompleteBackoff = time.Minute

// retryAfterMargin is added to the wait the API asks for before a retry, so
// the retry does not land on the last second of the cooldown.
const retryAfterMargin = time.Second

// maxRetryAfter bounds a retry_after_seconds the CLI is willing to act on; a
// larger value is treated as a wait that cannot fit.
const maxRetryAfter = 24 * time.Hour

// maxTooSoonRefusals is how many times in a row one retry may be refused with
// source_retry_too_soon before the refusal is reported as it is: the API keeps
// moving the time it asks for.
const maxTooSoonRefusals = 5

// pause waits for d or until ctx ends, whichever comes first. A variable so
// tests can run the pauses without a clock.
var pause = func(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// completeBackoff is the pause before the nth repeat (1-indexed): the wait
// interval, doubled each time. Deriving it from --wait-interval keeps the pacing
// in one flag and lets tests run without a clock. interval must be positive
// (the commands reject a flag that is not).
func completeBackoff(interval time.Duration, retry int) time.Duration {
	d := interval << retry
	if d <= 0 || d > maxCompleteBackoff {
		return maxCompleteBackoff
	}
	return d
}

// retryCap is how many repeats of 'complete' are allowed for this source: the
// API's own limit decides through `retryable` when the row reports one, and a
// private cap applies otherwise.
func retryCap(s *client.AppSource) int {
	if s.HasAttemptLimit() {
		return maxLimitedRetries
	}
	return maxCompleteRetries
}

// validationUnavailableError reports that the platform could not validate the
// archive and will not try again for this source: the API refused a retry
// because every attempt was used. It is a platform failure, not a verdict on
// the archive.
type validationUnavailableError struct {
	attempts, maxAttempts int
	cause                 *client.APIError
}

func (e *validationUnavailableError) Error() string {
	return fmt.Sprintf("validation unavailable after %d attempts", e.attempts)
}

// retryTooLateError reports that the API would accept a retry only after the
// time that is left for validation.
type retryTooLateError struct{ after time.Duration }

func (e *retryTooLateError) Error() string {
	return fmt.Sprintf("a retry is possible in %s", e.after)
}

// awaitValidation polls the source until validation reaches a verdict. A
// rejection the API marks retryable is not final: the archive was never judged,
// so 'complete' is repeated, pausing a little longer each time. A rejection that
// is not retryable is returned at once.
//
// The API paces the repeats too. A retry sent less than the cooldown after the
// rejection is refused with 409 source_retry_too_soon and retry_after_seconds:
// the call waits that long (and a margin) and sends 'complete' again, which is
// not counted as a repeat. A 409 source_retry_limit_reached means every
// validation attempt was used and ends the call with validationUnavailableError.
// A wait that does not fit in what is left of timeout ends it with
// retryTooLateError.
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
		if !source.IsRetryable() || retries >= retryCap(source) {
			return source, retries, nil
		}

		retries++
		if err := pause(ctx, completeBackoff(interval, retries)); err != nil {
			return source, retries - 1, timedOut(err)
		}
		if err := completeAgain(ctx, c, orgID, appID, sourceID); err != nil {
			return source, retries - 1, timedOut(err)
		}
	}
}

// completeAgain sends 'complete' for a rejected source and, while the API
// refuses it as too soon, waits as long as it asks and sends it again.
func completeAgain(ctx context.Context, c *client.Client, orgID, appID, sourceID string) error {
	for refusals := 0; ; refusals++ {
		_, err := c.CompleteSource(ctx, orgID, appID, sourceID)
		if err == nil {
			return nil
		}
		var apiErr *client.APIError
		if !errors.As(err, &apiErr) {
			return err
		}
		switch apiErr.ServerCode {
		case apiCodeRetryLimit:
			return &validationUnavailableError{attempts: apiErr.Attempts, maxAttempts: apiErr.MaxAttempts, cause: apiErr}
		case apiCodeRetryTooSoon:
			if refusals >= maxTooSoonRefusals {
				return classifySourceError(apiErr)
			}
			ask := min(time.Duration(apiErr.RetryAfterSeconds)*time.Second, maxRetryAfter)
			wait := max(ask+retryAfterMargin, time.Second)
			if deadline, ok := ctx.Deadline(); ok && wait > time.Until(deadline) {
				return &retryTooLateError{after: ask}
			}
			if err := pause(ctx, wait); err != nil {
				return err
			}
		default:
			return err
		}
	}
}

// platformGaveUp reports whether a rejected source is the platform's failure
// with no attempts left: the API says it used every attempt and the code names
// a cause that was not the archive's. The row's `retryable` is false then, which
// alone does not tell it from a rejection of the archive.
func platformGaveUp(s *client.AppSource) bool {
	return s != nil && s.Status == "rejected" && !s.IsRetryable() && s.AttemptsExhausted() &&
		s.ValidationCode != nil && platformValidationCodes[*s.ValidationCode]
}

// rejectionDetails is what a refused source adds to the error envelope.
func rejectionDetails(s *client.AppSource) map[string]any {
	d := map[string]any{"source_status": capString(s.Status)}
	if s.ValidationCode != nil && *s.ValidationCode != "" {
		d["validation_code"] = capString(*s.ValidationCode)
	}
	if s.Retryable != nil {
		d["retryable"] = *s.Retryable
	}
	if s.Attempts != nil {
		d["attempts"] = *s.Attempts
	}
	if s.MaxAttempts != nil {
		d["max_attempts"] = *s.MaxAttempts
	}
	return d
}
