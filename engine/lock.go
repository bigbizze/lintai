package engine

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bigbizze/lintai/bundle"
	"github.com/bigbizze/lintai/internal/atomicfile"
	"github.com/bigbizze/lintai/suppressions"
)

const DefaultRulesLockFile = "lintai-rules.lock.json"
const RulesLockFormat = "lintai.rules-lock/4"

type RulesLock struct {
	Rules          map[string]RulesLockRule
	Suppressions   suppressions.File
	target         string
	targetSnapshot atomicfile.Snapshot
	source         string
	sourceSnapshot atomicfile.Snapshot
}
type RulesLockRule struct {
	ID      string
	Version int
	Hash    string
}
type LockedRule struct {
	Path string
	Hash string
}
type lockRuleJSON struct {
	Path         string `json:"path"`
	ID           string `json:"id"`
	Version      int    `json:"version"`
	ArtifactHash string `json:"artifact_hash"`
}
type lockJSON struct {
	Format   string            `json:"format"`
	Rules    []lockRuleJSON    `json:"rules"`
	Approved suppressions.File `json:"approved_suppressions"`
}

func parseRulesLock(path string, data []byte) (RulesLock, error) {
	var raw lockJSON
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(&raw); err != nil {
		return RulesLock{}, fmt.Errorf("rules lock %s: %w", path, err)
	}
	if _, err := decoder.Token(); err != io.EOF {
		return RulesLock{}, fmt.Errorf("rules lock %s has trailing data", path)
	}
	if raw.Format != RulesLockFormat || raw.Rules == nil || raw.Approved == nil {
		return RulesLock{}, fmt.Errorf("rules lock %s has unsupported or invalid format %q", path, raw.Format)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return RulesLock{}, err
	}
	wrapped := append([]byte(`{"format":"lintai.suppressions/4","entries":`), fields["approved_suppressions"]...)
	wrapped = append(wrapped, '}')
	approved, err := suppressions.Parse(path, wrapped)
	if err != nil {
		return RulesLock{}, fmt.Errorf("rules lock %s has invalid approved suppressions: %w", path, err)
	}
	raw.Approved = approved
	for _, entry := range raw.Approved {
		for _, group := range entry.Groups {
			for _, reason := range group.Reasons {
				if err := suppressions.ValidateReason(reason.Value); err != nil {
					return RulesLock{}, fmt.Errorf("rules lock %s: suppression_policy_mismatch: %s/%s: %w", path, entry.Rule, entry.Assertion.Name, err)
				}
			}
		}
	}
	lock := RulesLock{Rules: make(map[string]RulesLockRule), Suppressions: raw.Approved}
	for _, rule := range raw.Rules {
		if rule.Path == "" || strings.Contains(rule.Path, `\`) || filepath.IsAbs(rule.Path) || filepath.Clean(rule.Path) != rule.Path || strings.HasPrefix(rule.Path, "../") || rule.ID == "" || rule.Version < 1 || !strings.HasPrefix(rule.ArtifactHash, "sha256:") || len(rule.ArtifactHash) != 71 {
			return RulesLock{}, fmt.Errorf("rules lock %s has invalid rule %q", path, rule.Path)
		}
		if _, err := hex.DecodeString(strings.TrimPrefix(rule.ArtifactHash, "sha256:")); err != nil || strings.ToLower(rule.ArtifactHash) != rule.ArtifactHash {
			return RulesLock{}, fmt.Errorf("rules lock %s has invalid artifact hash", path)
		}
		if _, ok := lock.Rules[rule.Path]; ok {
			return RulesLock{}, fmt.Errorf("rules lock %s repeats rule %s", path, rule.Path)
		}
		lock.Rules[rule.Path] = RulesLockRule{ID: rule.ID, Version: rule.Version, Hash: rule.ArtifactHash}
	}
	return lock, nil
}
func ReadRulesLock(path string) (RulesLock, error) {
	state, err := atomicfile.Read(path)
	if err != nil {
		return RulesLock{}, err
	}
	if !state.Exists {
		return RulesLock{}, fmt.Errorf("rules lock %s does not exist; run lintai lock to create it", path)
	}
	lock, err := parseRulesLock(path, state.Data)
	if err != nil {
		return RulesLock{}, err
	}
	lock.target, lock.targetSnapshot = path, state
	return lock, nil
}
func BuildRulesLock(ctx context.Context, options Options) (RulesLock, []LockedRule, error) {
	target := ResolveRulesLockPath(options.WorkspaceRoot, options.RulesLock)
	if options.RulesLock == "" {
		target = ResolveRulesLockPath(options.WorkspaceRoot, DefaultRulesLockFile)
	}
	targetState, err := atomicfile.Read(target)
	if err != nil {
		return RulesLock{}, nil, err
	}
	if targetState.Exists {
		if _, err := parseRulesLock(target, targetState.Data); err != nil {
			return RulesLock{}, nil, err
		}
	}
	source := suppressions.ResolvePath(options.WorkspaceRoot, options.SuppressionsLocation)
	sourceState, err := atomicfile.Read(source)
	if err != nil {
		return RulesLock{}, nil, err
	}
	if !sourceState.Exists && options.SuppressionsLocation != "" {
		return RulesLock{}, nil, fmt.Errorf("the suppressions file %s does not exist", source)
	}
	approved := suppressions.File{}
	if sourceState.Exists {
		approved, err = suppressions.Parse(source, sourceState.Data)
		if err != nil {
			return RulesLock{}, nil, err
		}
	}
	catalog, err := openRuleCatalog(ctx, options)
	if err != nil && catalog == nil {
		return RulesLock{}, nil, err
	}
	defer catalog.close()
	if catalog.bundleError != nil {
		return RulesLock{}, nil, catalog.bundleError
	}
	if len(catalog.bundleFailures) > 0 {
		messages := []string{}
		for name, failure := range catalog.bundleFailures {
			messages = append(messages, fmt.Sprintf("%s: %v", lockKey(options.WorkspaceRoot, name), failure))
		}
		sort.Strings(messages)
		return RulesLock{}, nil, fmt.Errorf("could not bundle rules: %s", strings.Join(messages, "; "))
	}
	catalog.loadMetadata(nil)
	lock := RulesLock{Rules: map[string]RulesLockRule{}, Suppressions: approved, target: target, targetSnapshot: targetState, source: source, sourceSnapshot: sourceState}
	locked := []LockedRule{}
	ids := map[string]bool{}
	for _, rulePath := range catalog.paths {
		entry := catalog.entries[rulePath]
		if entry.loadError != nil {
			return RulesLock{}, nil, entry.loadError
		}
		if !entry.isRule {
			continue
		}
		if ids[entry.metadata.ID] {
			return RulesLock{}, nil, fmt.Errorf("duplicate rule %s", entry.metadata.ID)
		}
		ids[entry.metadata.ID] = true
		key := lockKey(options.WorkspaceRoot, rulePath)
		lock.Rules[key] = RulesLockRule{ID: entry.metadata.ID, Version: entry.metadata.Version, Hash: entry.artifact.Hash}
		locked = append(locked, LockedRule{Path: key, Hash: entry.artifact.Hash})
	}
	return lock, locked, nil
}
func WriteRulesLock(ctx context.Context, path string, lock RulesLock) error {
	if lock.target != "" && filepath.Clean(lock.target) != filepath.Clean(path) {
		return fmt.Errorf("rules lock target changed")
	}
	rules := make([]lockRuleJSON, 0, len(lock.Rules))
	for path, rule := range lock.Rules {
		rules = append(rules, lockRuleJSON{path, rule.ID, rule.Version, rule.Hash})
	}
	sort.Slice(rules, func(i, j int) bool { return rules[i].Path < rules[j].Path })
	approved := lock.Suppressions
	if approved == nil {
		approved = suppressions.File{}
	}
	canonicalApproved, err := suppressions.Encode(approved)
	if err != nil {
		return err
	}
	approved, err = suppressions.Parse(path, canonicalApproved)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(lockJSON{RulesLockFormat, rules, approved}, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if _, err := parseRulesLock(path, data); err != nil {
		return err
	}
	return atomicfile.Write(ctx, path, lock.targetSnapshot, data, "rules_lock_changed", lock.source, lock.sourceSnapshot, "suppression_changed")
}
func ResolveRulesLockPath(root, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(root, path)
}

type integrityFailure struct {
	key     string
	message string
}

// checkRulesLock compares the bundled rules with the lock. It returns one
// failure for each rule whose hash differs, each rule that is not in the
// lock, and each locked rule that no longer exists. A rule that did not
// bundle already has a bundle error.
func checkRulesLock(lock RulesLock, workspaceRoot string, rulePaths []string, artifacts map[string]bundle.Artifacts) map[string]integrityFailure {
	failures := make(map[string]integrityFailure)
	seen := make(map[string]struct{}, len(rulePaths))
	for _, rulePath := range rulePaths {
		key := lockKey(workspaceRoot, rulePath)
		seen[key] = struct{}{}
		item, built := artifacts[rulePath]
		if !built {
			continue
		}
		locked, ok := lock.Rules[key]
		switch {
		case !ok:
			// A glob can also select helper modules; the catalog classifies
			// unlisted bundles before treating them as unapproved rules.
			continue
		case locked.Hash != item.Hash:
			failures[rulePath] = integrityFailure{key: key, message: fmt.Sprintf("rule %s bundle changed since the rules lock was written (locked %s, now %s); review the change and run lintai lock", key, locked.Hash, item.Hash)}
		}
	}
	for key := range lock.Rules {
		if _, ok := seen[key]; !ok {
			failures["locked:"+key] = integrityFailure{key: key, message: fmt.Sprintf("rule %s is in the rules lock but was not found; review the removal and run lintai lock", key)}
		}
	}
	return failures
}

// lockKey is the path of a rule file in the rules lock: relative to the
// workspace root, with forward slashes.
func lockKey(workspaceRoot, rulePath string) string {
	relative, err := filepath.Rel(workspaceRoot, rulePath)
	if err != nil {
		return filepath.ToSlash(rulePath)
	}
	return filepath.ToSlash(relative)
}

func assetRootOf(options Options) string {
	if options.AssetRoot != "" {
		return options.AssetRoot
	}
	return options.RepoRoot
}
