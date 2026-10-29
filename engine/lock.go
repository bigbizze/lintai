package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bigbizze/lintai/bundle"
)

// DefaultRulesLockFile is the name of the rules lock at the workspace root.
const DefaultRulesLockFile = "lintai-rules.lock.json"

// RulesLock is the content of a rules lock file. Rules maps the path of
// each rule file, relative to the workspace root, to the hash of every
// source file that the bundler read for the rule.
type RulesLock struct {
	Version int                      `json:"version"`
	Rules   map[string]RulesLockRule `json:"rules"`
}

// RulesLockRule is the locked hash of one rule.
type RulesLockRule struct {
	Hash string `json:"hash"`
}

// LockedRule is one rule that BuildRulesLock hashed.
type LockedRule struct {
	Path string
	Hash string
}

// BuildRulesLock bundles the rules that options select and returns the
// rules lock for them. A rule that does not bundle is an error.
func BuildRulesLock(ctx context.Context, options Options) (RulesLock, []LockedRule, error) {
	rulePaths, err := discoverRulePaths(options.WorkspaceRoot, options.RuleGlobs)
	if err != nil {
		return RulesLock{}, nil, err
	}
	bundleDir, err := os.MkdirTemp("", "lintai-bundle-*")
	if err != nil {
		return RulesLock{}, nil, err
	}
	defer os.RemoveAll(bundleDir)
	artifacts, failures, err := bundle.BuildAll(ctx, assetRootOf(options), options.RepoRoot, options.WorkspaceRoot, bundleDir, rulePaths)
	if err != nil {
		return RulesLock{}, nil, err
	}
	if len(failures) > 0 {
		messages := make([]string, 0, len(failures))
		for rulePath, failure := range failures {
			messages = append(messages, fmt.Sprintf("%s: %v", lockKey(options.WorkspaceRoot, rulePath), failure))
		}
		sort.Strings(messages)
		return RulesLock{}, nil, fmt.Errorf("could not bundle rules:\n%s", strings.Join(messages, "\n"))
	}
	lock := RulesLock{Version: 1, Rules: make(map[string]RulesLockRule, len(rulePaths))}
	locked := make([]LockedRule, 0, len(rulePaths))
	for _, rulePath := range rulePaths {
		key := lockKey(options.WorkspaceRoot, rulePath)
		hash := artifacts[rulePath].Hash
		lock.Rules[key] = RulesLockRule{Hash: hash}
		locked = append(locked, LockedRule{Path: key, Hash: hash})
	}
	return lock, locked, nil
}

// WriteRulesLock writes lock as indented JSON.
func WriteRulesLock(path string, lock RulesLock) error {
	data, err := json.MarshalIndent(lock, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// ReadRulesLock reads a rules lock file.
func ReadRulesLock(path string) (RulesLock, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return RulesLock{}, fmt.Errorf("rules lock %s does not exist; run lintai lock to create it", path)
		}
		return RulesLock{}, err
	}
	var lock RulesLock
	if err := json.Unmarshal(data, &lock); err != nil {
		return RulesLock{}, fmt.Errorf("rules lock %s is not valid JSON: %w", path, err)
	}
	if lock.Version != 1 {
		return RulesLock{}, fmt.Errorf("rules lock %s has version %d; this lintai reads version 1", path, lock.Version)
	}
	return lock, nil
}

// ResolveRulesLockPath returns path, resolved from the workspace root when
// it is relative.
func ResolveRulesLockPath(workspaceRoot, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(workspaceRoot, path)
}

// integrityFailure is a rule that does not match the rules lock.
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
			failures[rulePath] = integrityFailure{key: key, message: fmt.Sprintf("rule %s is not in the rules lock; review it and run lintai lock", key)}
		case locked.Hash != item.Hash:
			failures[rulePath] = integrityFailure{key: key, message: fmt.Sprintf("rule %s or a file it imports changed since the rules lock was written (locked %s, now %s); review the change and run lintai lock", key, locked.Hash, item.Hash)}
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
