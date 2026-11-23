package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/bigbizze/lintai/bundle"
	"github.com/bigbizze/lintai/observe"
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
	observations   observe.Set
	entries        map[string]catalogRule
	artifacts      map[string]bundle.Artifacts
	bundleFailures map[string]error
	bundleError    error
	bundleDir      string
	bundleBuilds   int
	metadataLoaded bool
}

type cachedRuleBundle struct {
	artifact    bundle.Artifacts
	pureCode    string
	prepareCode string
	context     string
}

func openRuleCatalog(ctx context.Context, options Options) (*ruleCatalog, error) {
	return openRuleCatalogWithCache(ctx, options, nil, nil)
}

func openRuleCatalogWithCache(ctx context.Context, options Options, engine *Session, lock *RulesLock) (*ruleCatalog, error) {
	if _, err := bundle.NodeExecutable(ctx); err != nil {
		return nil, err
	}
	paths, observations, err := discoverRulePaths(options.WorkspaceRoot, options.RuleGlobs, lock != nil && len(lock.Rules) > 0)
	if err != nil {
		return nil, err
	}
	catalog := &ruleCatalog{
		paths: paths, observations: observations, entries: make(map[string]catalogRule), artifacts: make(map[string]bundle.Artifacts),
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
	toolContext := assetRootOf(options) + "\x00" + options.RepoRoot + "\x00" + options.WorkspaceRoot
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
		if exists && cached.context == toolContext {
			changed, err := cached.artifact.Observations.Changed(ctx, observe.Full)
			if err != nil {
				catalog.bundleError = err
				return
			}
			if changed != nil {
				exists = false
			}
		}
		if !exists || cached.context != toolContext {
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
		pureCode, pureErr := os.ReadFile(artifact.PureBundlePath)
		prepareCode, prepareErr := os.ReadFile(artifact.PrepareBundlePath)
		if pureErr != nil || prepareErr != nil {
			continue
		}
		key := toolContext + "\x00" + rulePath
		engine.bundleCache[key] = cachedRuleBundle{
			artifact: bundle.Artifacts{Hash: artifact.Hash, Observations: artifact.Observations},
			pureCode: string(pureCode), prepareCode: string(prepareCode), context: toolContext,
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
		Hash: cached.artifact.Hash, Observations: cached.artifact.Observations,
	}, nil
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
