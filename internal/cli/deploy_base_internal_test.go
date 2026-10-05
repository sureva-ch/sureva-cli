package cli

import (
	"testing"

	"github.com/sureva-ch/sureva-cli/internal/client"
)

func TestIsUUID(t *testing.T) {
	t.Parallel()
	for s, want := range map[string]bool{
		"3c1b0d5e-4f6a-4a2b-9c3d-1e2f3a4b5c6d":          true,
		"3C1B0D5E-4F6A-4A2B-9C3D-1E2F3A4B5C6D":          true,
		"3c1b0d5e4f6a4a2b9c3d1e2f3a4b5c6d":              true,
		"{3c1b0d5e-4f6a-4a2b-9c3d-1e2f3a4b5c6d}":        true,
		"urn:uuid:3c1b0d5e-4f6a-4a2b-9c3d-1e2f3a4b5c6d": true,
		"":                                      false,
		"src-2-id":                              false,
		"3c1b0d5e-4f6a-4a2b-9c3d-1e2f3a4b5c6":   false, // one short
		"3c1b0d5e-4f6a-4a2b-9c3d-1e2f3a4b5c6dd": false, // one long
		"3c1b0d5e_4f6a-4a2b-9c3d-1e2f3a4b5c6d":  false, // wrong separator
		"3c1b0d5e-4f6a-4a2b-9c3d-1e2f3a4b5c6g":  false, // not hexadecimal
		"3c1b0d5e4f6a-4a2b-9c3d-1e2f3a4b5c6d00": false, // hyphens misplaced
	} {
		if got := isUUID(s); got != want {
			t.Errorf("isUUID(%q) = %v, want %v", s, got, want)
		}
	}
}

func TestLatestReady(t *testing.T) {
	t.Parallel()
	rows := []client.AppSource{
		{ID: "a", Seq: 9, Status: "rejected"},
		{ID: "b", Seq: 3, Status: "ready"},
		{ID: "c", Seq: 5, Status: "ready"},
		{ID: "d", Seq: 4, Status: "ready"},
	}
	if got := latestReady(rows); got == nil || got.ID != "c" {
		t.Errorf("latestReady = %+v, want c", got)
	}
	if latestReady(rows[:1]) != nil || latestReady(nil) != nil {
		t.Error("no ready row, no latest")
	}
}
