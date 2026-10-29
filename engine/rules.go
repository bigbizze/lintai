package engine

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/bigbizze/lintai/bundle"
	"github.com/bigbizze/lintai/runtime"
)

// ListedRule is a rule file and the id of its rule.
type ListedRule struct {
	ID   string `json:"id"`
	Path string `json:"path"`
}

// RuleLoadError is a rule file that did not bundle or load, or that has an
// id that another rule file has.
type RuleLoadError struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

// RuleList is the output of lintai rules.
type RuleList struct {
	Rules  []ListedRule    `json:"rules"`
	Errors []RuleLoadError `json:"errors"`
}

// ListRules bundles the rules that options select and reads the id of each
// rule from its pure bundle. It does not run setup(), build a snapshot or
// evaluate a program. A rule that does not bundle or load is in Errors. Two
// rule files with one id are both in Errors and not in Rules. A file without
// a default export is a helper module and is in neither list. Paths are
// relative to the workspace root. Rules is sorted by id, then path, and
// Errors by path, then message.
func ListRules(ctx context.Context, options Options) (RuleList, error) {
	rulePaths, err := discoverRulePaths(options.WorkspaceRoot, options.RuleGlobs)
	if err != nil {
		return RuleList{}, err
	}
	list := RuleList{Rules: []ListedRule{}, Errors: []RuleLoadError{}}
	if len(rulePaths) == 0 {
		return list, nil
	}
	bundleDir, err := os.MkdirTemp("", "lintai-bundle-*")
	if err != nil {
		return RuleList{}, err
	}
	defer os.RemoveAll(bundleDir)
	artifacts, failures, err := bundle.BuildAll(ctx, assetRootOf(options), options.RepoRoot, options.WorkspaceRoot, bundleDir, rulePaths)
	if err != nil {
		for _, rulePath := range rulePaths {
			message := err.Error()
			if failure, failed := failures[rulePath]; failed {
				message = failure.Error()
			}
			list.Errors = append(list.Errors, RuleLoadError{Path: lockKey(options.WorkspaceRoot, rulePath), Message: message})
		}
		sortRuleList(&list)
		return list, nil
	}
	for rulePath, failure := range failures {
		list.Errors = append(list.Errors, RuleLoadError{Path: lockKey(options.WorkspaceRoot, rulePath), Message: failure.Error()})
	}
	found := make([]ListedRule, 0, len(rulePaths))
	for _, rulePath := range rulePaths {
		item, built := artifacts[rulePath]
		if !built {
			continue
		}
		key := lockKey(options.WorkspaceRoot, rulePath)
		code, err := os.ReadFile(item.PureBundlePath)
		if err != nil {
			list.Errors = append(list.Errors, RuleLoadError{Path: key, Message: err.Error()})
			continue
		}
		id, isRule, err := runtime.ReadRuleID(string(code))
		if err != nil {
			list.Errors = append(list.Errors, RuleLoadError{Path: key, Message: err.Error()})
			continue
		}
		if isRule {
			found = append(found, ListedRule{ID: id, Path: key})
		}
	}
	pathsByID := make(map[string][]string, len(found))
	for _, item := range found {
		pathsByID[item.ID] = append(pathsByID[item.ID], item.Path)
	}
	for _, item := range found {
		paths := pathsByID[item.ID]
		if len(paths) > 1 {
			list.Errors = append(list.Errors, RuleLoadError{Path: item.Path, Message: duplicateIDMessage(item.ID, paths)})
			continue
		}
		list.Rules = append(list.Rules, item)
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
// the rules whose id another rule file has (phase config, one diagnostic
// for each file). Linter rule levels select rules by id, so two rules with
// one id cannot run.
func invalidPreparedRules(workspaceRoot string, prepared map[string]bundle.PrepareResult) map[string]preparedRuleFailure {
	failures := make(map[string]preparedRuleFailure)
	pathsByID := make(map[string][]string)
	for rulePath, item := range prepared {
		if err := runtime.ValidateRuleID(item.RuleID); err != nil {
			failures[rulePath] = preparedRuleFailure{ruleID: item.RuleID, phase: "setup", ruleVersion: item.RuleVersion, message: err.Error()}
			continue
		}
		pathsByID[item.RuleID] = append(pathsByID[item.RuleID], rulePath)
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
			failures[rulePath] = preparedRuleFailure{ruleID: id, phase: "config", ruleVersion: prepared[rulePath].RuleVersion, message: message}
		}
	}
	return failures
}
