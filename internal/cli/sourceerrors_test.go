package cli

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sureva-ch/sureva-cli/internal/client"
)

// What the server sends is echoed into the envelope, so it is bounded.
func TestAPIErrorDetails_CapsWhatItEchoes(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("é", 1000) // multi-byte: the cut must not split a character
	envs := make([]string, 100)
	for i := range envs {
		envs[i] = long
	}

	d := apiErrorDetails(&client.APIError{ServerCode: long, SourceStatus: long, ValidationCode: long, Environments: envs})

	for _, k := range []string{"api_code", "source_status", "validation_code"} {
		if got := []rune(d[k].(string)); len(got) != maxDetailString {
			t.Errorf("details.%s has %d characters, want %d", k, len(got), maxDetailString)
		}
	}
	got := d["environments"].([]string)
	if len(got) != maxDetailItems {
		t.Fatalf("details.environments has %d entries, want %d", len(got), maxDetailItems)
	}
	if n := len([]rune(got[0])); n != maxDetailString {
		t.Errorf("an environment has %d characters, want %d", n, maxDetailString)
	}
}

func TestAPIErrorDetails_ShortValuesAreUnchanged(t *testing.T) {
	t.Parallel()
	d := apiErrorDetails(&client.APIError{ServerCode: "source_retry_too_soon", SourceStatus: "rejected", Environments: []string{"production"},
		RetryAfterSeconds: 20, Attempts: 2, MaxAttempts: 3})
	if d["api_code"] != "source_retry_too_soon" || d["source_status"] != "rejected" || d["retry_after_seconds"] != 20 ||
		d["attempts"] != 2 || d["max_attempts"] != 3 || len(d["environments"].([]string)) != 1 {
		t.Errorf("details = %v", d)
	}
	if apiErrorDetails(&client.APIError{}) != nil {
		t.Error("an error without detail fields has no details")
	}
}

func TestRejectionDetails_CapsTheRow(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("x", 500)
	d := rejectionDetails(&client.AppSource{Status: long, ValidationCode: &long})
	if len(d["source_status"].(string)) != maxDetailString || len(d["validation_code"].(string)) != maxDetailString {
		t.Errorf("details = %v", d)
	}
}

func TestPause_StopsWhenTheContextEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	if err := pause(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Errorf("pause = %v, want context.Canceled", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Error("pause did not return promptly")
	}
	if err := pause(context.Background(), time.Millisecond); err != nil {
		t.Errorf("pause = %v, want nil", err)
	}
}
