package cli

import (
	"context"
	"time"
)

// SetPauseForTest replaces the pause between validation retries and returns a
// function that restores it. Tests use it to run the pauses without a clock.
func SetPauseForTest(fn func(ctx context.Context, d time.Duration) error) (restore func()) {
	prev := pause
	pause = fn
	return func() { pause = prev }
}
