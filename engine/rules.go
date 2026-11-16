package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/bigbizze/lintai/bundle"
	"github.com/bigbizze/lintai/runtime"
)

// ListedRule is a rule file and metadata from its loaded rule.
type ListedRule struct {
	ID             string   `json:"id"`
	Path           string   `json:"path"`
	Version        int      `json:"version"`
	Report         []string `json:"report"`
	ExpectNonEmpty []string `json:"expectNonEmpty"`
}

// RuleLoadError is a rule file that did not bundle or load, or that has an
// id that another rule file has. ID is set when the id of the rule is
// known and valid, so a linter plugin can still define a rule with that name.
type RuleLoadError struct {
	Path    string `json:"path"`
	ID      string `json:"id,omitempty"`
	Message string `json:"message"`
}

// RuleList is the output of lintai rules.
type RuleList struct {
	Rules  []ListedRule    `json:"rules"`
	Errors []RuleLoadError `json:"errors"`
}

type catalogRule struct {
	path      string
	artifact  bundle.Artifacts
	pureCode  string
	metadata  runtime.RuleMetadata
	isRule    bool
	loadError error
}

// ruleCatalog owns discovery, bundling and pure metadata loading for listing,
// lock creation and execution. It never runs setup or producer collection.
type ruleCatalog struct {
	paths          []string
	entries        map[string]catalogRule
	artifacts      map[string]bundle.Artifacts
	bundleFailures map[string]error
	bundleError    error
	bundleDir      string
	bundleBuilds   int
	metadataLoaded bool
}

type cachedRuleBundle struct {
	artifact       bundle.Artifacts
	pureCode       string
	prepareCode    string
	context        string
	resolutionDirs map[string]string
}

func openRuleCatalog(ctx context.Context, options Options) (*ruleCatalog, error) {
	return openRuleCatalogWithCache(ctx, options, nil)
}

func openRuleCatalogWithCache(ctx context.Context, options Options, engine *Session) (*ruleCatalog, error) {
	paths, err := discoverRulePaths(options.WorkspaceRoot, options.RuleGlobs)
	if err != nil {
		return nil, err
	}
	catalog := &ruleCatalog{
		paths: paths, entries: make(map[string]catalogRule), artifacts: make(map[string]bundle.Artifacts),
		bundleFailures: make(map[string]error),
	}
	if len(paths) == 0 {
		return catalog, nil
	}
	catalog.bundleDir, err = os.MkdirTemp("", "lintai-bundle-*")
	if err != nil {
		return nil, err
	}
	if engine == nil {
		catalog.artifacts, catalog.bundleFailures, catalog.bundleError = bundle.BuildAll(
			ctx, assetRootOf(options), options.RepoRoot, options.WorkspaceRoot, catalog.bundleDir, paths,
		)
		catalog.bundleBuilds = len(catalog.artifacts)
	} else {
		catalog.buildWithEngineCache(ctx, options, engine)
	}
	if catalog.artifacts == nil {
		catalog.artifacts = make(map[string]bundle.Artifacts)
	}
	if catalog.bundleFailures == nil {
		catalog.bundleFailures = make(map[string]error)
	}
	return catalog, catalog.bundleError
}

func (catalog *ruleCatalog) buildWithEngineCache(ctx context.Context, options Options, engine *Session) {
	toolContext, cacheable := bundleCacheContext(options)
	engine.bundleMu.Lock()
	defer engine.bundleMu.Unlock()
	if engine.bundleCache == nil {
		engine.bundleCache = make(map[string]cachedRuleBundle)
	}
	activeCacheKeys := make(map[string]struct{}, len(catalog.paths))
	for _, rulePath := range catalog.paths {
		activeCacheKeys[toolContext+"\x00"+rulePath] = struct{}{}
	}
	for key := range engine.bundleCache {
		if _, active := activeCacheKeys[key]; !active {
			delete(engine.bundleCache, key)
		}
	}
	catalog.artifacts = make(map[string]bundle.Artifacts, len(catalog.paths))
	catalog.bundleFailures = make(map[string]error)
	misses := make([]string, 0, len(catalog.paths))
	for index, rulePath := range catalog.paths {
		key := toolContext + "\x00" + rulePath
		cached, exists := engine.bundleCache[key]
		if !cacheable || !exists || cached.context != toolContext || !bundleInputsUnchanged(cached, options) {
			delete(engine.bundleCache, key)
			misses = append(misses, rulePath)
			continue
		}
		artifact, err := catalog.materializeCachedBundle(index, cached)
		if err != nil {
			delete(engine.bundleCache, key)
			misses = append(misses, rulePath)
			continue
		}
		catalog.artifacts[rulePath] = artifact
	}
	if len(misses) == 0 {
		return
	}
	buildDir := filepath.Join(catalog.bundleDir, "built")
	built, failures, buildErr := bundle.BuildAll(ctx, assetRootOf(options), options.RepoRoot, options.WorkspaceRoot, buildDir, misses)
	catalog.bundleBuilds = len(built)
	catalog.bundleError = buildErr
	for rulePath, failure := range failures {
		catalog.bundleFailures[rulePath] = failure
	}
	for _, rulePath := range misses {
		artifact, exists := built[rulePath]
		if !exists {
			continue
		}
		catalog.artifacts[rulePath] = artifact
		if !cacheable {
			continue
		}
		pureCode, pureErr := os.ReadFile(artifact.PureBundlePath)
		prepareCode, prepareErr := os.ReadFile(artifact.PrepareBundlePath)
		if pureErr != nil || prepareErr != nil {
			continue
		}
		resolutionDirs, dirsOK := bundleResolutionDirectories(artifact.Inputs, options)
		if !dirsOK {
			continue
		}
		key := toolContext + "\x00" + rulePath
		engine.bundleCache[key] = cachedRuleBundle{
			artifact: bundle.Artifacts{Hash: artifact.Hash, Inputs: cloneBundleInputs(artifact.Inputs)},
			pureCode: string(pureCode), prepareCode: string(prepareCode), context: toolContext,
			resolutionDirs: resolutionDirs,
		}
	}
}

func (catalog *ruleCatalog) materializeCachedBundle(index int, cached cachedRuleBundle) (bundle.Artifacts, error) {
	directory := filepath.Join(catalog.bundleDir, "cached", strconv.Itoa(index))
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return bundle.Artifacts{}, err
	}
	preparePath := filepath.Join(directory, "prepare.cjs")
	purePath := filepath.Join(directory, "pure.js")
	if err := os.WriteFile(preparePath, []byte(cached.prepareCode), 0o600); err != nil {
		return bundle.Artifacts{}, err
	}
	if err := os.WriteFile(purePath, []byte(cached.pureCode), 0o600); err != nil {
		return bundle.Artifacts{}, err
	}
	return bundle.Artifacts{
		PureBundlePath: purePath, PrepareBundlePath: preparePath,
		Hash: cached.artifact.Hash, Inputs: cloneBundleInputs(cached.artifact.Inputs),
	}, nil
}

func bundleCacheContext(options Options) (string, bool) {
	assetRoot := assetRootOf(options)
	script, err := resolveBundleScript(assetRoot)
	if err != nil {
		return "", false
	}
	files := []string{script, filepath.Join(assetRoot, "package.json"), filepath.Join(options.RepoRoot, "package.json"), filepath.Join(options.RepoRoot, "pnpm-lock.yaml")}
	for _, root := range []string{assetRoot, options.RepoRoot, options.WorkspaceRoot} {
		candidate := filepath.Join(root, "node_modules", "esbuild", "package.json")
		if _, err := os.Stat(candidate); err == nil {
			files = append(files, candidate)
			break
		}
	}
	var identity strings.Builder
	identity.WriteString(assetRoot)
	identity.WriteByte(0)
	identity.WriteString(options.RepoRoot)
	identity.WriteByte(0)
	identity.WriteString(options.WorkspaceRoot)
	for _, file := range files {
		contents, err := os.ReadFile(file)
		if err != nil {
			if file == script {
				return "", false
			}
			continue
		}
		digest := sha256.Sum256(contents)
		identity.WriteByte(0)
		identity.WriteString(file)
		identity.WriteByte(0)
		identity.WriteString(hex.EncodeToString(digest[:]))
	}
	digest := sha256.Sum256([]byte(identity.String()))
	return hex.EncodeToString(digest[:]), true
}

func resolveBundleScript(assetRoot string) (string, error) {
	return filepath.Abs(filepath.Join(assetRoot, "scripts", "bundle-rule.mjs"))
}

func bundleInputsUnchanged(cached cachedRuleBundle, options Options) bool {
	currentDirs, ok := bundleResolutionDirectories(cached.artifact.Inputs, options)
	if !ok || len(currentDirs) != len(cached.resolutionDirs) {
		return false
	}
	for directory, digest := range cached.resolutionDirs {
		if currentDirs[directory] != digest {
			return false
		}
	}
	return true
}

func bundleResolutionDirectories(inputs []bundle.Input, options Options) (map[string]string, bool) {
	directories := make(map[string]string)
	for _, input := range inputs {
		resolved := ""
		for _, candidate := range bundleInputCandidates(input.Path, options) {
			contents, err := os.ReadFile(candidate)
			if err != nil {
				continue
			}
			digest := sha256.Sum256(contents)
			if hex.EncodeToString(digest[:]) == input.SHA256 {
				resolved, _ = filepath.Abs(candidate)
				break
			}
		}
		if resolved == "" {
			return nil, false
		}
		boundary := bundleResolutionBoundary(resolved, options)
		for directory := filepath.Dir(resolved); ; directory = filepath.Dir(directory) {
			if _, exists := directories[directory]; exists {
				break
			}
			entries, err := os.ReadDir(directory)
			if err != nil {
				return nil, false
			}
			var names strings.Builder
			for _, entry := range entries {
				kind := "f"
				if entry.IsDir() {
					kind = "d"
				}
				names.WriteString(kind)
				names.WriteByte(':')
				names.WriteString(entry.Name())
				names.WriteByte(0)
				if !entry.IsDir() && (entry.Name() == "package.json" || entry.Name() == "jsconfig.json" || strings.HasPrefix(entry.Name(), "tsconfig") && strings.HasSuffix(entry.Name(), ".json")) {
					contents, err := os.ReadFile(filepath.Join(directory, entry.Name()))
					if err != nil {
						return nil, false
					}
					digest := sha256.Sum256(contents)
					names.Write(digest[:])
					names.WriteByte(0)
				}
			}
			digest := sha256.Sum256([]byte(names.String()))
			directories[directory] = hex.EncodeToString(digest[:])
			if directory == boundary || directory == filepath.Dir(directory) {
				break
			}
		}
	}
	return directories, true
}

func bundleResolutionBoundary(file string, options Options) string {
	roots := []string{options.WorkspaceRoot, options.RepoRoot, assetRootOf(options)}
	boundary := filepath.Dir(file)
	for _, root := range roots {
		if root == "" {
			continue
		}
		absolute, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		relative, err := filepath.Rel(absolute, file)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
			continue
		}
		if len(absolute) < len(boundary) {
			boundary = absolute
		}
	}
	return boundary
}

func bundleInputCandidates(inputPath string, options Options) []string {
	switch {
	case strings.HasPrefix(inputPath, "asset:"):
		return []string{filepath.Clean(filepath.Join(assetRootOf(options), strings.TrimPrefix(inputPath, "asset:")))}
	case strings.HasPrefix(inputPath, "node_modules/"):
		relative := filepath.FromSlash(inputPath)
		candidates := make([]string, 0, 3)
		for _, root := range []string{options.WorkspaceRoot, options.RepoRoot, assetRootOf(options)} {
			if root != "" {
				candidates = append(candidates, filepath.Join(root, relative))
			}
		}
		return candidates
	default:
		return []string{filepath.Join(options.WorkspaceRoot, filepath.FromSlash(inputPath))}
	}
}

func cloneBundleInputs(inputs []bundle.Input) []bundle.Input {
	return append([]bundle.Input(nil), inputs...)
}

func (catalog *ruleCatalog) loadMetadata(skip map[string]struct{}) {
	if catalog == nil || catalog.metadataLoaded {
		return
	}
	catalog.metadataLoaded = true
	for _, rulePath := range catalog.paths {
		if _, skipped := skip[rulePath]; skipped {
			continue
		}
		artifact, built := catalog.artifacts[rulePath]
		if !built {
			continue
		}
		entry := catalogRule{path: rulePath, artifact: artifact}
		code, readErr := os.ReadFile(artifact.PureBundlePath)
		if readErr != nil {
			entry.loadError = readErr
			catalog.entries[rulePath] = entry
			continue
		}
		entry.pureCode = string(code)
		entry.metadata, entry.isRule, entry.loadError = runtime.ReadRuleMetadata(entry.pureCode)
		catalog.entries[rulePath] = entry
	}
}

func (catalog *ruleCatalog) close() {
	if catalog != nil && catalog.bundleDir != "" {
		_ = os.RemoveAll(catalog.bundleDir)
		catalog.bundleDir = ""
	}
}

func (catalog *ruleCatalog) rulePathsWithArtifacts() []string {
	paths := make([]string, 0, len(catalog.artifacts))
	for _, rulePath := range catalog.paths {
		if _, exists := catalog.artifacts[rulePath]; exists {
			paths = append(paths, rulePath)
		}
	}
	return paths
}

// ListRules bundles the rules that options select and reads validated
// metadata from each loaded pure bundle. It does not run setup(), facts(),
// program() or message(), build a snapshot or evaluate a program. A rule
// that does not bundle or load is in Errors. A rule with a valid id that is
// not a complete rule (no program(), report() or message()) is in Errors
// with its id. Two rule files with one id are both in Errors with the id and
// not in Rules. A file without a default export is a helper module and is in
// neither list. Paths are relative to the workspace root. Rules is sorted
// by id, then path, and Errors by path, then message.
func ListRules(ctx context.Context, options Options) (RuleList, error) {
	catalog, err := openRuleCatalog(ctx, options)
	if err != nil {
		if catalog == nil {
			return RuleList{}, err
		}
	}
	defer catalog.close()
	list := RuleList{Rules: []ListedRule{}, Errors: []RuleLoadError{}}
	if len(catalog.paths) == 0 {
		return list, nil
	}
	catalog.loadMetadata(nil)
	for rulePath, failure := range catalog.bundleFailures {
		list.Errors = append(list.Errors, RuleLoadError{Path: lockKey(options.WorkspaceRoot, rulePath), Message: failure.Error()})
	}
	type foundRule struct {
		ListedRule
		shapeErr error
	}
	found := make([]foundRule, 0, len(catalog.entries))
	for _, rulePath := range catalog.paths {
		entry, loaded := catalog.entries[rulePath]
		if !loaded {
			if catalog.bundleError != nil {
				list.Errors = append(list.Errors, RuleLoadError{Path: lockKey(options.WorkspaceRoot, rulePath), Message: catalog.bundleError.Error()})
			}
			continue
		}
		key := lockKey(options.WorkspaceRoot, rulePath)
		if entry.loadError != nil {
			message := entry.loadError.Error()
			if entry.metadata.ID != "" && runtime.ValidateRuleID(entry.metadata.ID) == nil {
				found = append(found, foundRule{ListedRule: ListedRule{ID: entry.metadata.ID, Path: key}, shapeErr: entry.loadError})
			} else {
				list.Errors = append(list.Errors, RuleLoadError{Path: key, Message: message})
			}
			continue
		}
		if !entry.isRule {
			continue
		}
		listed := ListedRule{
			ID: entry.metadata.ID, Path: key, Version: entry.metadata.Version,
			Report:         append([]string{}, entry.metadata.Report...),
			ExpectNonEmpty: append([]string{}, entry.metadata.ExpectNonEmpty...),
		}
		found = append(found, foundRule{ListedRule: listed})
	}
	pathsByID := make(map[string][]string, len(found))
	for _, item := range found {
		pathsByID[item.ID] = append(pathsByID[item.ID], item.Path)
	}
	for _, item := range found {
		paths := pathsByID[item.ID]
		if len(paths) > 1 {
			list.Errors = append(list.Errors, RuleLoadError{Path: item.Path, ID: item.ID, Message: duplicateIDMessage(item.ID, paths)})
			continue
		}
		if item.shapeErr != nil {
			list.Errors = append(list.Errors, RuleLoadError{Path: item.Path, ID: item.ID, Message: item.shapeErr.Error()})
			continue
		}
		list.Rules = append(list.Rules, item.ListedRule)
	}
	sortRuleList(&list)
	return list, nil
}

func sortRuleList(list *RuleList) {
	sort.Slice(list.Rules, func(left, right int) bool {
		if list.Rules[left].ID != list.Rules[right].ID {
			return list.Rules[left].ID < list.Rules[right].ID
		}
		return list.Rules[left].Path < list.Rules[right].Path
	})
	sort.Slice(list.Errors, func(left, right int) bool {
		if list.Errors[left].Path != list.Errors[right].Path {
			return list.Errors[left].Path < list.Errors[right].Path
		}
		return list.Errors[left].Message < list.Errors[right].Message
	})
}

// duplicateIDMessage names every rule file that has the id.
func duplicateIDMessage(id string, paths []string) string {
	sorted := append([]string(nil), paths...)
	sort.Strings(sorted)
	return fmt.Sprintf("rule id %q is used by more than one rule file (%s); each rule needs its own id", id, strings.Join(sorted, ", "))
}

// preparedRuleFailure is a prepared rule that must not run.
type preparedRuleFailure struct {
	ruleID      string
	phase       string
	ruleVersion int
	message     string
}

// invalidPreparedRules returns the prepared rules whose id does not match
// runtime.RuleIDPattern (phase setup, as the other rule shape errors) and
// the prepared rules whose id another rule file has (phase config). Linter
// rule levels select rules by id, so two rules with one id cannot run. A
// rule whose setup() failed still has its id, so it counts as a duplicate
// too, as in lintai rules, which does not run setup().
func invalidPreparedRules(workspaceRoot string, prepared map[string]bundle.PrepareResult, prepareFailures map[string]bundle.PrepareError) map[string]preparedRuleFailure {
	failures := make(map[string]preparedRuleFailure)
	pathsByID := make(map[string][]string)
	for rulePath, item := range prepared {
		if err := runtime.ValidateRuleID(item.RuleID); err != nil {
			failures[rulePath] = preparedRuleFailure{ruleID: item.RuleID, phase: "setup", ruleVersion: item.RuleVersion, message: err.Error()}
			continue
		}
		pathsByID[item.RuleID] = append(pathsByID[item.RuleID], rulePath)
	}
	for rulePath, item := range prepareFailures {
		if item.RuleID != "" {
			pathsByID[item.RuleID] = append(pathsByID[item.RuleID], rulePath)
		}
	}
	for id, rulePaths := range pathsByID {
		if len(rulePaths) < 2 {
			continue
		}
		keys := make([]string, 0, len(rulePaths))
		for _, rulePath := range rulePaths {
			keys = append(keys, lockKey(workspaceRoot, rulePath))
		}
		message := duplicateIDMessage(id, keys)
		for _, rulePath := range rulePaths {
			item, ready := prepared[rulePath]
			if !ready {
				continue
			}
			failures[rulePath] = preparedRuleFailure{ruleID: id, phase: "config", ruleVersion: item.RuleVersion, message: message}
		}
	}
	return failures
}
