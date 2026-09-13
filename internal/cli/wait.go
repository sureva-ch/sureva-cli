package cli

import (
	"context"
	"errors"
	"time"
)

// errWaitTimeout is the sentinel returned by pollUntil when the timeout elapses
// before the polling function reports done. Callers distinguish it from API
// errors via errors.Is(err, errWaitTimeout).
var errWaitTimeout = errors.New("wait timeout")

// pollUntil calls fn repeatedly at the given interval until fn returns done=true,
// fn returns a non-nil error, the context is cancelled, or timeout elapses.
//
// On timeout it returns errWaitTimeout.
// On context cancellation it returns the context error.
// When fn returns an error it propagates that error immediately.
//
// The interval and timeout are driven by flags (--wait-interval, --wait-timeout)
// so tests can pass tiny values (e.g. 1ms) without any clock injection.
func pollUntil(ctx context.Context, interval, timeout time.Duration, fn func(context.Context) (done bool, err error)) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return errWaitTimeout
			}
			return ctx.Err()
		case <-ticker.C:
			done, err := fn(ctx)
			if err != nil {
				// If the internal deadline expired while fn was executing, surface
				// the result as errWaitTimeout rather than the context error — the
				// deadline is the reason the poll stopped, not an API failure.
				if errors.Is(ctx.Err(), context.DeadlineExceeded) {
					return errWaitTimeout
				}
				return err
			}
			if done {
				return nil
			}
		}
	}
}
