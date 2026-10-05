// Package sourcebase records which release a pulled directory was based on.
// `sureva sources pull` writes the record and `sureva deploy` reads it, so a
// later push can tell what the tree started from.
package sourcebase

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
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

// maxRecordBytes bounds how much of the record is read. A real one is a few
// hundred bytes.
const maxRecordBytes = 4096

// StateError means the state location cannot be used safely: the state
// directory or the record is a symlink or another kind of file than expected.
// Nothing is ever written through it.
type StateError struct {
	Path   string
	Reason string
}

func (e *StateError) Error() string {
	return fmt.Sprintf("%s %s; remove it or choose another --dir", e.Path, e.Reason)
}

// Check reports whether Write could store a record in dir: the state directory,
// when it exists, must be a real directory and the record, when it exists, a
// regular file. Nothing is created or changed, so a caller can refuse before
// doing any work.
func Check(dir string) error {
	sd := filepath.Join(dir, Dir)
	fi, err := os.Lstat(sd)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return err
	case !fi.IsDir():
		return &StateError{Path: sd, Reason: "exists and is not a plain directory (a symlink or a file)"}
	}
	fi, err = os.Lstat(Path(dir))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return err
	case !fi.Mode().IsRegular():
		return &StateError{Path: Path(dir), Reason: "exists and is not a regular file (a symlink or a directory)"}
	}
	return nil
}

// Write stores b in dir/.sureva/source.json and returns the path. It never
// writes through a link: the state directory and an existing record are checked
// with Lstat, and the record is written to a new file in the same directory and
// renamed into place, so a crash leaves the old record or none, never a
// truncated one.
func Write(dir string, b Base) (string, error) {
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return "", err
	}
	if err := Check(dir); err != nil {
		return "", err
	}
	sd := filepath.Join(dir, Dir)
	if err := os.Mkdir(sd, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
		return "", err
	}
	// Check again now that the directory exists: it must be the real one.
	if err := Check(dir); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(sd, ".source-*.tmp") // O_EXCL, mode 0600
	if err != nil {
		return "", err
	}
	_, werr := tmp.Write(append(data, '\n'))
	cerr := tmp.Close()
	if err := errors.Join(werr, cerr); err != nil {
		_ = os.Remove(tmp.Name())
		return "", err
	}
	p := Path(dir)
	if err := os.Rename(tmp.Name(), p); err != nil {
		_ = os.Remove(tmp.Name())
		return "", err
	}
	return p, nil
}

// Read returns the record of dir, or nil when there is none or it cannot be
// read as one: a missing file, a symlink or other non-regular file, one larger
// than a record can be, or malformed JSON. That only means "base unknown", so
// it is never an error for the caller to act on.
func Read(dir string) *Base {
	p := Path(dir)
	fi, err := os.Lstat(p)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() > maxRecordBytes {
		return nil
	}
	f, err := os.Open(p)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, maxRecordBytes+1))
	if err != nil || len(data) > maxRecordBytes {
		return nil
	}
	var b Base
	if json.Unmarshal(data, &b) != nil || b.SourceID == "" {
		return nil
	}
	return &b
}
