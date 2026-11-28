package engine

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"github.com/bigbizze/lintai/diagnostics"
	"github.com/bigbizze/lintai/internal/canonical"
	"github.com/bigbizze/lintai/observe"
)

// FileGroup is one document: its name under the physical workspace root, the
// SHA-256 of the analyzed logical text in lowercase hexadecimal (empty when no
// source identity was verified), and its findings.
type FileGroup struct {
	File         string
	SourceDigest string
	Diagnostics  []diagnostics.Diagnostic
}

// FilesResult contains document groups and workspace problems from one
// completed publication. PhysicalRoot is the session's workspace directory.
type FilesResult struct {
	Files             []FileGroup
	PhysicalRoot      string
	WorkspaceProblems []diagnostics.Diagnostic
	FactRevision      string
	RulesLoaded       int
	Warnings          []string
}

type publishedResult struct {
	result        Result
	sourceDigests map[string]string
	observations  observe.Set
	reusable      bool
}

// fileIndex groups the diagnostics of one completed result by document. It
// is built once and read by every lookup of that result.
type fileIndex struct {
	root      string
	byFile    map[string][]int
	unlocated []int
}

// normalizeConfig detaches the session policy and resolves its paths once.
func normalizeConfig(config Config) (Config, error) {
	if config.RuleWorkers < 0 {
		return Config{}, fmt.Errorf("rule workers must be zero or a positive integer")
	}
	given, err := filepath.Abs(config.WorkspaceRoot)
	if err != nil {
		return Config{}, err
	}
	physical, err := filepath.EvalSymlinks(given)
	if err != nil {
		return Config{}, fmt.Errorf("workspace root: %w", err)
	}
	if info, err := os.Stat(physical); err != nil {
		return Config{}, fmt.Errorf("workspace root: %w", err)
	} else if !info.IsDir() {
		return Config{}, fmt.Errorf("workspace root %s is not a directory", given)
	}
	config.WorkspaceRoot = physical
	if config.RulesLock != "" {
		config.RulesLock = filepath.Clean(RebasePath(given, config.WorkspaceRoot, ResolveRulesLockPath(config.WorkspaceRoot, config.RulesLock)))
	}
	if config.SuppressionsLocation != "" {
		config.SuppressionsLocation = filepath.Clean(RebasePath(given, config.WorkspaceRoot, ResolveRulesLockPath(config.WorkspaceRoot, config.SuppressionsLocation)))
	}
	if config.FactLimit <= 0 {
		config.FactLimit = DefaultFactLimit
	}
	policies := make(map[string]RulePolicy, len(config.Rule))
	for id, policy := range config.Rule {
		if policy.Severity == "" {
			policy.Severity = "error"
		}
		if _, err := diagnostics.ParseSeverity(policy.Severity); err != nil {
			return Config{}, fmt.Errorf("rule %s: %w", id, err)
		}
		if policy.Options == nil {
			policy.Options = map[string]any{}
		}
		raw, err := canonical.NormalizeStructured(policy.Options)
		if err != nil {
			return Config{}, fmt.Errorf("rule %s options: %w", id, err)
		}
		policy.Options = raw.(map[string]any)
		policies[id] = policy
	}
	config.Rule = policies
	return config, nil
}

func newPublishedResult(result Result, root string, sourceDigests map[string]string, observations observe.Set, reusable bool) *publishedResult {
	index := &fileIndex{root: root, byFile: make(map[string][]int)}
	for _, file := range result.AnalyzedFiles {
		index.byFile[documentName(root, file)] = nil
	}
	for i, item := range result.Diagnostics {
		if item.DiagnosticKind == diagnostics.KindRuleExecutionError {
			reusable = false
		}
		if item.SourceLocation == nil {
			index.unlocated = append(index.unlocated, i)
		} else {
			file := documentName(root, item.SourceLocation.File)
			index.byFile[file] = append(index.byFile[file], i)
		}
	}
	result.index = index
	return &publishedResult{result: result, sourceDigests: sourceDigests, observations: observations, reusable: reusable}
}

// verifiedSourceDigests keeps a document's digest only when every producer
// that owns a reported range in it captured the same text. Otherwise the host
// receives no digest and treats the group as unverified.
func verifiedSourceDigests(result Result, root string, evidence *executionEvidence) map[string]string {
	verified := maps.Clone(evidence.sourceDigests)
	for _, item := range result.Diagnostics {
		if item.SourceLocation == nil || item.EntityIdentity == nil {
			continue
		}
		owner, anchored := evidence.anchorOwners[item.EntityIdentity.SemanticKey]
		if !anchored {
			continue
		}
		file := documentName(root, item.SourceLocation.File)
		if _, captured := evidence.digestOwners[file][owner]; !captured {
			delete(verified, file)
		}
	}
	return verified
}

// documentName names a file under the physical workspace root: relative to
// the root inside it, otherwise its clean absolute path, with forward slashes.
// A relative name is based at the root.
func documentName(root, file string) string {
	name := filepath.Clean(filepath.FromSlash(file))
	if !filepath.IsAbs(name) {
		name = filepath.Join(root, name)
	}
	if relative, err := filepath.Rel(root, name); err == nil && filepath.IsLocal(relative) {
		name = relative
	}
	return filepath.ToSlash(name)
}

func (p *publishedResult) group(file string) FileGroup {
	index := p.result.index
	group := FileGroup{File: file, Diagnostics: []diagnostics.Diagnostic{}}
	indexes, captured := index.byFile[file]
	if captured {
		group.SourceDigest = p.sourceDigests[file]
	}
	for _, i := range indexes {
		group.Diagnostics = append(group.Diagnostics, p.result.Diagnostics[i])
	}
	group.Diagnostics = cloneDiagnostics(group.Diagnostics)
	return group
}

// project returns the named documents, or every document when all is set,
// with the workspace problems of the publication.
func (p *publishedResult) project(files []string, all bool) FilesResult {
	index := p.result.index
	if all {
		files = slices.Sorted(maps.Keys(index.byFile))
	}
	result := FilesResult{
		Files: make([]FileGroup, 0, len(files)), PhysicalRoot: index.root,
		FactRevision: p.result.FactRevision, RulesLoaded: p.result.RulesLoaded,
		Warnings: slices.Clone(p.result.Warnings),
	}
	for _, file := range files {
		result.Files = append(result.Files, p.group(file))
	}
	for _, i := range index.unlocated {
		result.WorkspaceProblems = append(result.WorkspaceProblems, p.result.Diagnostics[i])
	}
	result.WorkspaceProblems = cloneDiagnostics(result.WorkspaceProblems)
	return result
}
