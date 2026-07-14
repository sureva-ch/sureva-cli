package changes

import (
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// buildImportEdges resolves internal import edges for the changed files across
// supported languages. Resolution is best-effort: a file that cannot be read or
// parsed simply contributes no edges instead of failing the whole graph. Only
// imports that resolve to files existing in the repository become edges, so
// third-party dependencies are naturally excluded.
func buildImportEdges(repoRoot string, changes []Change) []Edge {
	goModule := ""
	if module, err := ReadModulePath(repoRoot); err == nil {
		goModule = module
	}
	seen := make(map[Edge]bool)
	var edges []Edge
	for _, change := range uniqueSortedChanges(changes) {
		if change.Status == StatusDeleted {
			continue
		}
		for _, target := range resolveImports(repoRoot, goModule, change.Path) {
			if target == "" || target == change.Path {
				continue
			}
			edge := Edge{From: change.Path, To: target}
			if seen[edge] {
				continue
			}
			seen[edge] = true
			edges = append(edges, edge)
		}
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].From == edges[j].From {
			return edges[i].To < edges[j].To
		}
		return edges[i].From < edges[j].From
	})
	return edges
}

// resolveImports dispatches to the resolver for the file's language.
func resolveImports(repoRoot, goModule, changed string) []string {
	switch strings.ToLower(filepath.Ext(changed)) {
	case ".go":
		if goModule == "" {
			return nil
		}
		targets, err := resolveGoImports(repoRoot, goModule, changed)
		if err != nil {
			return nil
		}
		return targets
	case ".js", ".jsx", ".ts", ".tsx", ".mjs", ".cjs", ".mts", ".cts", ".astro", ".vue", ".svelte":
		return resolveJSImports(repoRoot, changed)
	case ".py":
		return resolvePyImports(repoRoot, changed)
	default:
		return nil
	}
}

func resolveGoImports(repoRoot, modulePath, changed string) ([]string, error) {
	imports, err := parseFileImports(filepath.Join(repoRoot, filepath.FromSlash(changed)))
	if err != nil {
		return nil, err
	}
	var targets []string
	for _, importPath := range imports {
		if !isInternalImport(modulePath, importPath) {
			continue
		}
		files, err := resolveImportFiles(repoRoot, modulePath, importPath)
		if err != nil {
			return nil, err
		}
		targets = append(targets, files...)
	}
	return targets, nil
}

// --- JavaScript / TypeScript ---

var jsSpecPatterns = []*regexp.Regexp{
	regexp.MustCompile(`\bfrom\s*['"]([^'"]+)['"]`),
	regexp.MustCompile(`\brequire\s*\(\s*['"]([^'"]+)['"]\s*\)`),
	regexp.MustCompile(`\bimport\s*\(\s*['"]([^'"]+)['"]\s*\)`),
	regexp.MustCompile(`(?m)^\s*import\s+['"]([^'"]+)['"]`),
}

var jsExtensions = []string{".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs", ".mts", ".cts", ".astro", ".vue", ".svelte", ".d.ts"}

func resolveJSImports(repoRoot, changed string) []string {
	data, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(changed)))
	if err != nil {
		return nil
	}
	src := string(data)
	specs := make(map[string]bool)
	for _, re := range jsSpecPatterns {
		for _, match := range re.FindAllStringSubmatch(src, -1) {
			specs[match[1]] = true
		}
	}
	dir := path.Dir(changed)
	var targets []string
	for spec := range specs {
		if !strings.HasPrefix(spec, ".") {
			continue // bare specifier: third-party or alias, not a repo file
		}
		if target := resolveJSFile(repoRoot, path.Join(dir, spec)); target != "" {
			targets = append(targets, target)
		}
	}
	sort.Strings(targets)
	return targets
}

func resolveJSFile(repoRoot, base string) string {
	candidates := []string{base}
	for _, ext := range jsExtensions {
		candidates = append(candidates, base+ext)
	}
	for _, ext := range jsExtensions {
		candidates = append(candidates, path.Join(base, "index")+ext)
	}
	for _, candidate := range candidates {
		if fileExists(repoRoot, candidate) {
			return candidate
		}
	}
	return ""
}

// --- Python ---

var (
	pyFromPattern   = regexp.MustCompile(`(?m)^\s*from\s+(\.*[\w.]*)\s+import\s+(.+)$`)
	pyImportPattern = regexp.MustCompile(`(?m)^\s*import\s+([\w][\w.]*(?:\s*,\s*[\w][\w.]*)*)`)
)

func resolvePyImports(repoRoot, changed string) []string {
	data, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(changed)))
	if err != nil {
		return nil
	}
	src := string(data)
	fileDir := path.Dir(changed)
	targets := make(map[string]bool)

	for _, match := range pyFromPattern.FindAllStringSubmatch(src, -1) {
		module, names := match[1], match[2]
		baseParts := pyBaseParts(fileDir, module)
		base := path.Join(baseParts...)
		for _, target := range existingModuleFiles(repoRoot, base) {
			targets[target] = true
		}
		// `from <pkg> import name` — a name may itself be a submodule.
		for _, name := range splitPyNames(names) {
			for _, target := range existingModuleFiles(repoRoot, path.Join(base, name)) {
				targets[target] = true
			}
		}
	}
	for _, match := range pyImportPattern.FindAllStringSubmatch(src, -1) {
		for _, module := range splitPyNames(match[1]) {
			base := path.Join(pyBaseParts(fileDir, module)...)
			for _, target := range existingModuleFiles(repoRoot, base) {
				targets[target] = true
			}
		}
	}

	out := make([]string, 0, len(targets))
	for target := range targets {
		out = append(out, target)
	}
	sort.Strings(out)
	return out
}

// pyBaseParts converts a Python module spec into repo-relative path parts.
// Leading dots mean a relative import: one dot is the file's own package, each
// extra dot walks up a directory. A dot-free spec resolves from the repo root.
func pyBaseParts(fileDir, module string) []string {
	level := 0
	for level < len(module) && module[level] == '.' {
		level++
	}
	rest := module[level:]
	var parts []string
	if level > 0 {
		dir := fileDir
		for i := 0; i < level-1; i++ {
			dir = path.Dir(dir)
		}
		if dir != "." && dir != "" {
			parts = append(parts, strings.Split(dir, "/")...)
		}
	}
	if rest != "" {
		parts = append(parts, strings.Split(rest, ".")...)
	}
	return parts
}

func splitPyNames(s string) []string {
	s = strings.Trim(strings.TrimSpace(s), "()")
	var out []string
	for _, part := range strings.Split(s, ",") {
		fields := strings.Fields(strings.TrimSpace(part))
		if len(fields) == 0 || fields[0] == "*" {
			continue
		}
		out = append(out, fields[0])
	}
	return out
}

// existingModuleFiles returns the module file and package init that exist for a
// repo-relative base path (without extension).
func existingModuleFiles(repoRoot, base string) []string {
	if base == "" || base == "." {
		return nil
	}
	var out []string
	for _, candidate := range []string{base + ".py", path.Join(base, "__init__.py")} {
		if fileExists(repoRoot, candidate) {
			out = append(out, candidate)
		}
	}
	return out
}

func fileExists(repoRoot, rel string) bool {
	info, err := os.Stat(filepath.Join(repoRoot, filepath.FromSlash(rel)))
	return err == nil && !info.IsDir()
}
