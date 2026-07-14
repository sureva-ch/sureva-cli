# Contributing to Sureva CLI

## Quick path

1. Use Go 1.25.11 or later.
2. Create a focused branch and keep generated or unrelated changes out.
3. Run `gofmt -w` on changed Go files.
4. Run `go test ./...`, `go vet ./...`, and `go build ./...`.
5. Open a pull request explaining the behavior change and its verification.

## Standards

- Use Conventional Commits.
- Keep tests with the behavior they prove; use `t.TempDir()` for filesystem tests.
- Preserve JSON stdout and JSON error-envelope contracts for automation.
- Never commit tokens, credentials, local config files, or generated secrets.
- Update `README.md` and `CHANGELOG.md` for user-visible changes.

By contributing, you agree that your contribution is licensed under the MIT
License in [LICENSE](LICENSE).
