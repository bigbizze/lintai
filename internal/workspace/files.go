package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// ScopeOptions describes the ordered selection policy for primary source
// files. Exclude is the workspace-config layer; InvocationExclude is the
// higher-priority CLI layer.
type ScopeOptions struct {
	Exclude           []string
	Include           []string
	Sources           []string
	InvocationExclude []string
}

type DirectoryMembership struct {
	Path    string
	Entries []string
}

type ScopeMatch struct {
	Path            string
	ExcludePatterns []string
	IncludePatterns []string
	Exact           bool
}

// ScopePlan is the result of the production source selector. Paths are
// canonical absolute paths; membership is the directory evidence used to
// detect a newly eligible file on validation.
type ScopePlan struct {
	Root       string
	Files      []string
	ExactFiles []string
	Pruned     []string
	Membership []DirectoryMembership
	Matches    []ScopeMatch
	Revision   string
	Options    ScopeOptions
}

// PlanSources applies built-in, configured and invocation selection layers,
// then adds validated exact sources. Discovery does not follow symlinks.
func PlanSources(root string, options ScopeOptions) (*ScopePlan, error) {
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace root: %w", err)
	}
	absoluteRoot = filepath.Clean(absoluteRoot)
	if resolved, err := filepath.EvalSymlinks(absoluteRoot); err == nil {
		absoluteRoot = resolved
	}
	info, err := os.Stat(absoluteRoot)
	if err != nil {
		return nil, fmt.Errorf("workspace root %s: %w", absoluteRoot, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("workspace root %s is not a directory", absoluteRoot)
	}
	if err := validatePatterns(options); err != nil {
		return nil, err
	}
	plan := &ScopePlan{Root: filepath.Clean(absoluteRoot), Options: cloneScopeOptions(options)}
	exact, err := resolveExactSources(absoluteRoot, options.Sources)
	if err != nil {
		return nil, err
	}
	plan.ExactFiles = exact
	exactRelative := make([]string, 0, len(exact))
	for _, path := range exact {
		exactRelative = append(exactRelative, relativeSlash(absoluteRoot, path))
	}
	exactSet := make(map[string]struct{}, len(exact))
	for _, path := range exact {
		exactSet[path] = struct{}{}
	}
	selected := make(map[string]struct{})
	var walk func(string, string) error
	walk = func(directory, relative string) error {
		entries, err := os.ReadDir(directory)
		if err != nil {
			return fmt.Errorf("read source membership %s: %w", directory, err)
		}
		membership := DirectoryMembership{Path: filepath.ToSlash(directory)}
		for _, entry := range entries {
			kind := "f:"
			if entry.Type()&fs.ModeSymlink != 0 {
				kind = "s:"
			} else if entry.IsDir() {
				kind = "d:"
			}
			if entry.IsDir() || entry.Type()&fs.ModeSymlink != 0 || isSupportedExactSource(entry.Name()) {
				membership.Entries = append(membership.Entries, kind+entry.Name())
			}
		}
		plan.Membership = append(plan.Membership, membership)
		for _, entry := range entries {
			nextRel := entry.Name()
			if relative != "" {
				nextRel = relative + "/" + entry.Name()
			}
			path := filepath.Join(directory, entry.Name())
			if entry.Type()&fs.ModeSymlink != 0 {
				continue
			}
			if entry.IsDir() {
				if isGitMetadata(nextRel) {
					plan.Pruned = append(plan.Pruned, nextRel)
					continue
				}
				if shouldPruneDirectory(nextRel, options, exactRelative) {
					plan.Pruned = append(plan.Pruned, nextRel)
					continue
				}
				if err := walk(path, nextRel); err != nil {
					return err
				}
				continue
			}
			if !isSourceFile(path) || isGitMetadata(nextRel) || excludedAtInvocation(nextRel, options.InvocationExclude) {
				continue
			}
			if defaultExcluded(nextRel) || matchesAny(options.Exclude, nextRel) {
				if !matchesAny(options.Include, nextRel) {
					continue
				}
			}
			canonical := filepath.Clean(path)
			selected[canonical] = struct{}{}
		}
		return nil
	}
	if err := walk(absoluteRoot, ""); err != nil {
		return nil, err
	}
	for _, path := range exact {
		selected[path] = struct{}{}
	}
	plan.Files = sortedPaths(selected)
	for _, path := range plan.Files {
		relative := relativeSlash(absoluteRoot, path)
		_, isExact := exactSet[path]
		plan.Matches = append(plan.Matches, ScopeMatch{
			Path: relative, ExcludePatterns: matchingPatterns(options.Exclude, relative),
			IncludePatterns: matchingPatterns(options.Include, relative), Exact: isExact,
		})
	}
	slices.Sort(plan.Pruned)
	plan.Pruned = slices.Compact(plan.Pruned)
	slices.SortFunc(plan.Membership, func(a, b DirectoryMembership) int { return strings.Compare(a.Path, b.Path) })
	for i := range plan.Membership {
		slices.Sort(plan.Membership[i].Entries)
	}
	plan.Revision = scopeRevision(plan)
	return plan, nil
}

// ValidateScope replans with the same production selector and compares the
// complete selection and traversed directory memberships.
func ValidateScope(plan *ScopePlan) (bool, error) {
	current, err := PlanSources(plan.Root, plan.Options)
	if err != nil {
		return false, err
	}
	return current.Revision == plan.Revision, nil
}

// ListSourceFiles preserves the small helper used by callers and tests while
// routing it through the same selector used in production.
func ListSourceFiles(root string, excludes []string) ([]string, error) {
	plan, err := PlanSources(root, ScopeOptions{InvocationExclude: excludes})
	if err != nil {
		return nil, err
	}
	return plan.Files, nil
}

func validatePatterns(options ScopeOptions) error {
	for _, layer := range []struct {
		name     string
		patterns []string
	}{{"workspace exclude", options.Exclude}, {"workspace include", options.Include}, {"invocation exclude", options.InvocationExclude}} {
		for _, pattern := range layer.patterns {
			if pattern == "" || filepath.IsAbs(pattern) || strings.HasPrefix(pattern, "../") || strings.Contains(pattern, "\\") || !doublestar.ValidatePattern(pattern) {
				return fmt.Errorf("invalid %s pattern %q", layer.name, pattern)
			}
		}
	}
	return nil
}

func resolveExactSources(root string, names []string) ([]string, error) {
	byPath := make(map[string]struct{}, len(names))
	for _, name := range names {
		if name == "" {
			return nil, fmt.Errorf("exact source path must not be empty")
		}
		path := name
		if !filepath.IsAbs(path) {
			if filepath.IsAbs(filepath.FromSlash(name)) {
				return nil, fmt.Errorf("exact source %q is not workspace-relative", name)
			}
			path = filepath.Join(root, filepath.FromSlash(name))
		}
		path = filepath.Clean(path)
		if !withinRoot(root, path) || isGitMetadata(relativeSlash(root, path)) {
			return nil, fmt.Errorf("exact source %q is outside the workspace or inside .git", name)
		}
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			return nil, fmt.Errorf("exact source %q: %w", name, err)
		}
		resolved = filepath.Clean(resolved)
		if !withinRoot(root, resolved) || isGitMetadata(relativeSlash(root, resolved)) {
			return nil, fmt.Errorf("exact source %q resolves outside the workspace or inside .git", name)
		}
		info, err := os.Stat(resolved)
		if err != nil {
			return nil, fmt.Errorf("exact source %q: %w", name, err)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("exact source %q is not a regular file", name)
		}
		if !isSupportedExactSource(resolved) {
			return nil, fmt.Errorf("exact source %q has an unsupported extension", name)
		}
		byPath[resolved] = struct{}{}
	}
	return sortedPaths(byPath), nil
}

func isSupportedExactSource(path string) bool {
	if strings.HasSuffix(path, ".d.ts") {
		return true
	}
	switch filepath.Ext(path) {
	case ".ts", ".tsx", ".js", ".jsx":
		return true
	default:
		return false
	}
}

func isSourceFile(path string) bool {
	if strings.HasSuffix(path, ".d.ts") {
		return false
	}
	switch filepath.Ext(path) {
	case ".ts", ".tsx", ".js", ".jsx":
		return true
	default:
		return false
	}
}

func defaultExcluded(relative string) bool {
	for _, part := range strings.Split(filepath.ToSlash(relative), "/") {
		if part == "node_modules" || part == "dist" || strings.HasPrefix(part, ".") {
			return true
		}
	}
	return false
}

func isGitMetadata(relative string) bool {
	for _, part := range strings.Split(filepath.ToSlash(relative), "/") {
		if part == ".git" {
			return true
		}
	}
	return false
}

func shouldPruneDirectory(relative string, options ScopeOptions, exact []string) bool {
	if hasExactBelow(relative, exact) {
		return false
	}
	if excludedAtInvocation(relative, options.InvocationExclude) {
		return true
	}
	if defaultExcluded(relative) {
		return !anyPatternCouldMatchBelow(options.Include, relative)
	}
	if matchesAny(options.Exclude, relative) {
		return !anyPatternCouldMatchBelow(options.Include, relative)
	}
	return false
}

func anyPatternCouldMatchBelow(patterns []string, directory string) bool {
	for _, pattern := range patterns {
		if patternCouldMatchBelow(pattern, directory) {
			return true
		}
	}
	return false
}

func patternCouldMatchBelow(pattern, directory string) bool {
	if patternMatches(pattern, directory) {
		return true
	}
	meta := strings.IndexAny(pattern, "*?[]{}")
	prefix := pattern
	if meta >= 0 {
		prefix = pattern[:meta]
	}
	prefix = strings.TrimSuffix(prefix, "/")
	if prefix == "" || directory == prefix || strings.HasPrefix(prefix, directory+"/") || strings.HasPrefix(directory, prefix+"/") {
		return true
	}
	return false
}

func hasExactBelow(directory string, exact []string) bool {
	directory += "/"
	for _, path := range exact {
		relative := filepath.ToSlash(path)
		if strings.HasPrefix(relative, directory) {
			return true
		}
	}
	return false
}

func excludedAtInvocation(path string, patterns []string) bool {
	return matchesAny(patterns, path)
}

func matchesAny(patterns []string, relative string) bool {
	for _, pattern := range patterns {
		if patternMatches(pattern, relative) {
			return true
		}
	}
	return false
}

func matchingPatterns(patterns []string, relative string) []string {
	result := make([]string, 0)
	for _, pattern := range patterns {
		if patternMatches(pattern, relative) {
			result = append(result, pattern)
		}
	}
	slices.Sort(result)
	return slices.Compact(result)
}

func patternMatches(pattern, relative string) bool {
	relative = filepath.ToSlash(relative)
	if ok, _ := doublestar.Match(pattern, relative); ok {
		return true
	}
	if ok, _ := doublestar.Match(pattern, relative+"/"); ok {
		return true
	}
	parts := strings.Split(relative, "/")
	for end := 1; end < len(parts); end++ {
		prefix := strings.Join(parts[:end], "/")
		if ok, _ := doublestar.Match(pattern, prefix); ok {
			return true
		}
		if ok, _ := doublestar.Match(pattern, prefix+"/"); ok {
			return true
		}
	}
	return false
}

func cloneScopeOptions(options ScopeOptions) ScopeOptions {
	return ScopeOptions{
		Exclude: slices.Clone(options.Exclude), Include: slices.Clone(options.Include),
		Sources: slices.Clone(options.Sources), InvocationExclude: slices.Clone(options.InvocationExclude),
	}
}

func scopeRevision(plan *ScopePlan) string {
	var builder strings.Builder
	write := func(value string) {
		builder.WriteString(fmt.Sprintf("%d:", len(value)))
		builder.WriteString(value)
	}
	for _, layer := range [][]string{plan.Options.Exclude, plan.Options.Include, plan.Options.Sources, plan.Options.InvocationExclude} {
		builder.WriteString("[")
		values := slices.Clone(layer)
		slices.Sort(values)
		values = slices.Compact(values)
		for _, value := range values {
			write(value)
		}
		builder.WriteString("]")
	}
	for _, layer := range [][]string{plan.Files, plan.ExactFiles} {
		builder.WriteString("[")
		for _, value := range layer {
			write(relativeSlash(plan.Root, value))
		}
		builder.WriteString("]")
	}
	builder.WriteString("[")
	for _, value := range plan.Pruned {
		write(value)
	}
	builder.WriteString("]")
	for _, directory := range plan.Membership {
		write(relativeSlash(plan.Root, directory.Path))
		for _, entry := range directory.Entries {
			write(entry)
		}
	}
	for _, match := range plan.Matches {
		write(match.Path)
		for _, pattern := range match.ExcludePatterns {
			write("exclude:" + pattern)
		}
		for _, pattern := range match.IncludePatterns {
			write("include:" + pattern)
		}
		write(fmt.Sprint(match.Exact))
	}
	digest := sha256.Sum256([]byte(builder.String()))
	return hex.EncodeToString(digest[:])
}

func sortedPaths(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for path := range values {
		result = append(result, path)
	}
	slices.Sort(result)
	return result
}

func withinRoot(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}

func relativeSlash(root, path string) string {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(relative)
}

func RelativePath(root, path string) string { return relativeSlash(root, path) }

func ReadFile(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}
