// Package sourcebase records which release a pulled directory was based on.
// `sureva sources pull` writes the record and `sureva deploy` reads it, so a
// later push can tell what the tree started from.
package sourcebase

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

const (
	// Dir is the state directory inside a pulled tree. It is never packed.
	Dir = ".sureva"
	// File is the record inside Dir.
	File = "source.json"
)

// Base is the release a directory was pulled from.
type Base struct {
	AppID      string    `json:"app_id"`
	SourceID   string    `json:"source_id"`
	ReleaseTag string    `json:"release_tag"`
	SHA256     string    `json:"sha256"`
	PulledAt   time.Time `json:"pulled_at"`
}

// Path returns where the record of dir lives.
func Path(dir string) string {
	return filepath.Join(dir, Dir, File)
}

// Write stores b in dir/.sureva/source.json and returns the path.
func Write(dir string, b Base) (string, error) {
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Join(dir, Dir), 0o755); err != nil {
		return "", err
	}
	p := Path(dir)
	if err := os.WriteFile(p, append(data, '\n'), 0o644); err != nil {
		return "", err
	}
	return p, nil
}

// Read returns the record of dir, or nil when there is none or it cannot be
// read as one. A missing or damaged record only means "base unknown", so it is
// never an error for the caller to act on.
func Read(dir string) *Base {
	data, err := os.ReadFile(Path(dir))
	if err != nil {
		return nil
	}
	var b Base
	if json.Unmarshal(data, &b) != nil || b.SourceID == "" {
		return nil
	}
	return &b
}
