package cli

import (
	"context"
	"errors"
	"testing"
	"time"
)

// ---- T7: pollUntil RED tests ----
// Note: these tests are in package cli (not cli_test) to access unexported pollUntil.

func TestPollUntil_ReturnsDone(t *testing.T) {
	t.Parallel()

	calls := 0
	err := pollUntil(context.Background(), 1*time.Millisecond, 1*time.Second, func(ctx context.Context) (bool, error) {
		calls++
		if calls >= 3 {
			return true, nil
		}
		return false, nil
	})

	if err != nil {
		t.Fatalf("pollUntil: expected nil error, got %v", err)
	}
	if calls < 3 {
		t.Errorf("fn calls = %d, want >= 3 (must poll until done)", calls)
	}
}

func TestPollUntil_ReturnsTimeoutOnDeadline(t *testing.T) {
	t.Parallel()

	err := pollUntil(context.Background(), 1*time.Millisecond, 5*time.Millisecond, func(ctx context.Context) (bool, error) {
		return false, nil // never done
	})

	if err == nil {
		t.Fatal("pollUntil: expected errWaitTimeout, got nil")
	}
	if !errors.Is(err, errWaitTimeout) {
		t.Errorf("pollUntil: got %v, want errWaitTimeout sentinel", err)
	}
}

func TestPollUntil_PropagatesFnError(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("fn exploded")
	err := pollUntil(context.Background(), 1*time.Millisecond, 1*time.Second, func(ctx context.Context) (bool, error) {
		return false, sentinel
	})

	if err == nil {
		t.Fatal("pollUntil: expected fn error, got nil")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("pollUntil: got %v, want sentinel", err)
	}
}

func TestPollUntil_RespectsContextCancel(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())

	calls := 0
	go func() {
		time.Sleep(5 * time.Millisecond)
		cancel()
	}()

	err := pollUntil(ctx, 1*time.Millisecond, 10*time.Second, func(ctx context.Context) (bool, error) {
		calls++
		return false, nil
	})

	if err == nil {
		t.Fatal("pollUntil: expected error on ctx cancel, got nil")
	}
	if errors.Is(err, errWaitTimeout) {
		t.Errorf("pollUntil: got errWaitTimeout, want context error (not a timeout)")
	}
}
