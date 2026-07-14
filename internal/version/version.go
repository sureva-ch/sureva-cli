// Package version holds the build-time metadata injected via ldflags.
//
// At release, goreleaser sets these variables:
//
//	-X github.com/sureva-ch/sureva-cli/internal/version.Version=v1.2.3
//	-X github.com/sureva-ch/sureva-cli/internal/version.Commit=abc1234
//	-X github.com/sureva-ch/sureva-cli/internal/version.BuiltAt=2026-01-01T00:00:00Z
package version

// Version is the semantic version string. Overridden by goreleaser ldflags; "dev" otherwise.
var Version = "dev"

// Commit is the short git commit SHA. Overridden by goreleaser ldflags; "unknown" otherwise.
var Commit = "unknown"

// BuiltAt is the RFC3339 build timestamp. Overridden by goreleaser ldflags; "unknown" otherwise.
var BuiltAt = "unknown"
