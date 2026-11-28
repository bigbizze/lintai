package engine

import (
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/bigbizze/lintai/diagnostics"
	"github.com/bigbizze/lintai/internal/canonical"
	"github.com/bigbizze/lintai/observe"
)

// FileResult is the projection of a completed workspace result for one file.
type FileResult struct {
	SourceDigest      string
	Diagnostics       []diagnostics.Diagnostic
	WorkspaceProblems []diagnostics.Diagnostic
	SnapshotVersion   string
	RulesLoaded       int
	Warnings          []string
}

// FilesResult contains ordered projections and workspace problems from one
// completed publication. Diagnostics[i] corresponds to the ith requested file.
type FilesResult struct {
	Files []string
	// SourceDigests[i] is the SHA-256 of the analyzed logical text, in lowercase
	// hexadecimal, or empty if the generation supplied no source identity.
	SourceDigests     []string
	Diagnostics       [][]diagnostics.Diagnostic
	WorkspaceProblems []diagnostics.Diagnostic
	SnapshotVersion   string
	RulesLoaded       int
	Warnings          []string
}

type publishedResult struct {
	result        Result
	sourceDigests map[string]string
	observations  observe.Set
	reusable      bool
	byFile        map[string][]int
	unlocated     []int
}

// normalizeConfig detaches the session policy and resolves its paths once.
func normalizeConfig(config Config) (Config, error) {
	if config.RuleWorkers < 0 {
		return Config{}, fmt.Errorf("rule workers must be zero or a positive integer")
	}
	var err error
	if config.WorkspaceRoot, err = filepath.Abs(config.WorkspaceRoot); err != nil {
		return Config{}, err
	}
	if config.RulesLock != "" {
		config.RulesLock = filepath.Clean(ResolveRulesLockPath(config.WorkspaceRoot, config.RulesLock))
	}
	if config.SuppressionsLocation != "" && !filepath.IsAbs(config.SuppressionsLocation) {
		config.SuppressionsLocation = filepath.Join(config.WorkspaceRoot, config.SuppressionsLocation)
	}
	if config.SuppressionsLocation != "" {
		config.SuppressionsLocation = filepath.Clean(config.SuppressionsLocation)
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

func newPublishedResult(result Result, sourceDigests map[string]string, observations observe.Set, reusable bool) *publishedResult {
	published := &publishedResult{result: result, sourceDigests: sourceDigests, observations: observations, reusable: reusable, byFile: make(map[string][]int)}
	for _, file := range result.AnalyzedFiles {
		published.byFile[result.files.key(file)] = nil
	}
	for i, item := range result.Diagnostics {
		if item.DiagnosticKind == diagnostics.KindRuleExecutionError {
			published.reusable = false
		}
		if item.SourceLocation == nil {
			published.unlocated = append(published.unlocated, i)
		} else {
			file := result.files.key(item.SourceLocation.File)
			published.byFile[file] = append(published.byFile[file], i)
		}
	}
	return published
}

// verifiedSourceDigests keeps a file's digest only when every producer that
// owns a reported range in that file captured the same text. Otherwise the
// host receives no digest and treats the group as unverified.
func verifiedSourceDigests(result Result, evidence *executionEvidence) map[string]string {
	verified := maps.Clone(evidence.sourceDigests)
	for _, item := range result.Diagnostics {
		if item.SourceLocation == nil || item.EntityIdentity == nil {
			continue
		}
		owner, anchored := evidence.anchorOwners[item.EntityIdentity.SemanticKey]
		if !anchored {
			continue
		}
		key := result.files.key(item.SourceLocation.File)
		if _, captured := evidence.digestOwners[key][owner]; !captured {
			delete(verified, key)
		}
	}
	return verified
}

// fileKeys maps every accepted spelling of a published file to one key:
// workspace-relative through the requested root or the physical root captured
// once for the operation, then through an exact-source alias. A path outside
// the workspace stays absolute.
type fileKeys struct {
	root, physical string
	aliases        map[string]string
}

func newFileKeys(root string, aliases map[string]string) fileKeys {
	physical, err := filepath.EvalSymlinks(root)
	if err != nil {
		physical = root
	}
	return fileKeys{root: root, physical: filepath.Clean(physical), aliases: aliases}
}

func (k fileKeys) key(file string) string {
	clean := filepath.Clean(filepath.FromSlash(file))
	if filepath.IsAbs(clean) && k.physical != k.root {
		if relative, err := filepath.Rel(k.physical, clean); err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			clean = relative
		}
	}
	key := publicationFileKey(k.root, clean)
	if canonical, ok := k.aliases[key]; ok {
		return canonical
	}
	return key
}

func publicationFileKey(root, file string) string {
	file = filepath.Clean(filepath.FromSlash(file))
	if filepath.IsAbs(file) {
		if relative, err := filepath.Rel(root, file); err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			file = relative
		}
	}
	return filepath.ToSlash(file)
}

func (p *publishedResult) projectFiles(files []string, all bool) FilesResult {
	if all {
		files = slices.Sorted(maps.Keys(p.byFile))
		for alias, file := range p.result.files.aliases {
			if _, published := p.byFile[file]; published {
				files = append(files, alias)
			}
		}
		slices.Sort(files)
		files = slices.Compact(files)
	}
	result := FilesResult{
		Files: append([]string{}, files...), SourceDigests: make([]string, len(files)),
		Diagnostics:     make([][]diagnostics.Diagnostic, len(files)),
		SnapshotVersion: p.result.SnapshotVersion, RulesLoaded: p.result.RulesLoaded,
		Warnings: slices.Clone(p.result.Warnings),
	}
	for i, file := range files {
		key := p.result.files.key(file)
		indexes, captured := p.byFile[key]
		if captured {
			result.SourceDigests[i] = p.sourceDigests[key]
		}
		for _, index := range indexes {
			result.Diagnostics[i] = append(result.Diagnostics[i], p.result.Diagnostics[index])
		}
		result.Diagnostics[i] = cloneDiagnostics(result.Diagnostics[i])
	}
	for _, index := range p.unlocated {
		result.WorkspaceProblems = append(result.WorkspaceProblems, p.result.Diagnostics[index])
	}
	result.WorkspaceProblems = cloneDiagnostics(result.WorkspaceProblems)
	return result
}
