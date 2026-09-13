package changes

import "regexp"

// Classification patterns for the readiness checklist. A single file may match
// more than one (e.g. CHANGELOG.md is both a doc and the changelog), so each
// signal is independent.
var (
	reChangelog = regexp.MustCompile(`(?i)^change[-_]?log(\.[a-z0-9]+)?$`)
	reDoc       = regexp.MustCompile(`(?i)(\.(md|mdx|rst|adoc|txt)$|(^|/)docs?/|(^|/)readme(\.[a-z0-9]+)?$)`)
	reTest      = regexp.MustCompile(`(?i)(_test\.go$|\.(test|spec)\.[jt]sx?$|(^|/)tests?/|(^|/)__tests__/|(^|/)test_[^/]+\.py$|_test\.py$)`)
	reMigration = regexp.MustCompile(`(?i)((^|/)(migrations|db/(migrate|migrations)|database/migrations)/|(^|/)(db|database|migrations)/.*changelog[^/]*\.(xml|ya?ml|json)$|(^|/)((?:\d{8,14}[_-][^/]+\.(sql|rb)|\d{1,6}[_-][^/]+\.sql)|v\d+(?:_\d+)*__[^/]+\.sql|u\d+(?:_\d+)*__[^/]+\.sql|r__[^/]+\.sql)$)`)
	reCode      = regexp.MustCompile(`(?i)\.(go|js|jsx|ts|tsx|mjs|cjs|mts|cts|py|rb|rs|java|kt|kts|c|cc|cpp|h|hpp|cs|php|swift|scala|astro|vue|svelte)$`)
)

// buildChecklist derives readiness signals from the changed file paths: whether
// the change touches source code, tests, documentation, and a changelog.
func buildChecklist(nodes []string) []ChecklistItem {
	var code, tests, docs, changelog, migrations bool
	for _, node := range nodes {
		isChangelog := reChangelog.MatchString(node)
		isTest := reTest.MatchString(node)
		isMigration := reMigration.MatchString(node)
		if isChangelog {
			changelog = true
		}
		if isTest {
			tests = true
		}
		if isMigration {
			migrations = true
		}
		if !isChangelog && reDoc.MatchString(node) {
			docs = true
		}
		if !isTest && !isMigration && reCode.MatchString(node) {
			code = true
		}
	}
	return []ChecklistItem{
		{Key: "code", Label: "Source code files", OK: code},
		{Key: "tests", Label: "Test files", OK: tests},
		{Key: "docs", Label: "Documentation files", OK: docs},
		{Key: "migrations", Label: "Database migrations", OK: migrations},
		{Key: "changelog", Label: "Changelog entry", OK: changelog},
	}
}
