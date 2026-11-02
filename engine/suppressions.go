package engine

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bigbizze/lintai/diagnostics"
	"github.com/bigbizze/lintai/facts"
	"github.com/bigbizze/lintai/suppressions"
)

// runInfo is what Analyze needs from the rule evaluation to apply the
// suppressions file.
type runInfo struct {
	lock *RulesLock
	// files are the files that have location facts in the snapshot, as
	// suppressions.NormalizePath keys.
	files map[string]struct{}
	// evaluated are the rules that ran with severity error and had no
	// rule execution error.
	evaluated map[string]struct{}
	// known are the ids of every loaded rule.
	known map[string]struct{}
	// complete is true when every rule file bundled and matched the rules
	// lock, so known has every rule id.
	complete        bool
	snapshotVersion string
	backendID       string
}

// suppressionsState is the suppressions file that a run reads.
type suppressionsState struct {
	path   string
	file   suppressions.File
	exists bool
}

func validateSuppressionsOptions(options Options) error {
	switch {
	case options.SuppressAll && len(options.SuppressRules) > 0:
		return fmt.Errorf("--suppress-all and --suppress-rule cannot be used together")
	case options.SuppressAll && options.PruneSuppressions:
		return fmt.Errorf("--suppress-all and --prune-suppressions cannot be used together")
	case len(options.SuppressRules) > 0 && options.PruneSuppressions:
		return fmt.Errorf("--suppress-rule and --prune-suppressions cannot be used together")
	}
	return nil
}

// loadSuppressions reads the suppressions file before the analysis, so an
// invalid file fails the run before the rules run. A missing file at the
// default location suppresses nothing. A missing file at a location that
// the caller gave is an error, unless the run records suppressions.
func loadSuppressions(options Options) (suppressionsState, error) {
	if err := validateSuppressionsOptions(options); err != nil {
		return suppressionsState{}, err
	}
	path := suppressions.ResolvePath(options.WorkspaceRoot, options.SuppressionsLocation)
	file, err := suppressions.Load(path)
	switch {
	case err == nil:
		return suppressionsState{path: path, file: file, exists: true}, nil
	case errors.Is(err, suppressions.ErrNotExist):
		if options.SuppressionsLocation != "" && !options.SuppressAll && len(options.SuppressRules) == 0 {
			return suppressionsState{}, fmt.Errorf("the suppressions file %s does not exist; run lintai with --suppress-all or --suppress-rule to create it", path)
		}
		return suppressionsState{path: path, file: suppressions.File{}}, nil
	default:
		return suppressionsState{}, err
	}
}

// snapshotFiles returns the files that have location facts.
func snapshotFiles(snapshot facts.Snapshot) map[string]struct{} {
	files := make(map[string]struct{})
	for _, fact := range snapshot.Facts {
		if fact.Relation != facts.LocationRelation || len(fact.Values) < 2 {
			continue
		}
		files[suppressions.NormalizePath(fact.Values[1].String)] = struct{}{}
	}
	return files
}

// applySuppressions records or prunes suppressions when options ask for it,
// checks the suppressions against the rules lock, and removes the
// suppressed violations from result.Diagnostics.
func applySuppressions(options Options, state suppressionsState, run runInfo, result *Result) error {
	result.SuppressionsPath = state.path
	var unknownRules []diagnostics.Diagnostic
	if len(options.SuppressRules) > 0 && run.complete {
		unknownRules = unknownSuppressRules(options, run)
	}
	allRulesLoaded := len(options.RuleGlobs) == 1 &&
		filepath.Clean(options.RuleGlobs[0]) == filepath.Clean("lintai-rules/**/*.ts")
	if len(options.RuleGlobs) == 1 && filepath.IsAbs(options.RuleGlobs[0]) {
		allRulesLoaded = filepath.Clean(options.RuleGlobs[0]) == filepath.Join(options.WorkspaceRoot, "lintai-rules", "**", "*.ts")
	}
	allRulesLoaded = allRulesLoaded && (options.Severity == "" || options.Severity == diagnostics.SeverityError)
	for _, item := range result.Diagnostics {
		if item.DiagnosticKind == diagnostics.KindRuleExecutionError {
			allRulesLoaded = false
			break
		}
	}
	scope := suppressions.Scope{
		Files:          run.files,
		Rules:          run.evaluated,
		KnownRules:     run.known,
		AllRulesLoaded: allRulesLoaded,
	}
	file := state.file
	if options.SuppressAll || len(options.SuppressRules) > 0 || options.PruneSuppressions {
		if len(unknownRules) > 0 {
			ids := make([]string, 0, len(unknownRules))
			for _, item := range unknownRules {
				ids = append(ids, item.RuleID)
			}
			result.Warnings = append(result.Warnings, fmt.Sprintf("lintai did not update the suppressions file %s, because --suppress-rule names rules that are not loaded: %s", state.path, strings.Join(ids, ", ")))
		} else if blocked := blockingRuleErrors(options, result.Diagnostics, run.known); blocked > 0 {
			result.Warnings = append(result.Warnings, fmt.Sprintf("lintai did not update the suppressions file %s, because %d rules could not run; fix the rule errors and run the command again", state.path, blocked))
		} else {
			var updated suppressions.File
			switch {
			case options.SuppressAll:
				updated = suppressions.Record(file, result.Diagnostics, nil)
			case len(options.SuppressRules) > 0:
				updated = suppressions.Record(file, result.Diagnostics, options.SuppressRules)
			default:
				updated = suppressions.Prune(file, result.Diagnostics, scope, func(filePath string) bool {
					_, err := os.Stat(filepath.Join(options.WorkspaceRoot, filepath.FromSlash(filePath)))
					return !errors.Is(err, os.ErrNotExist)
				})
			}
			if options.SuppressAll || len(options.SuppressRules) > 0 || state.exists {
				if err := suppressions.Write(state.path, updated); err != nil {
					return err
				}
				result.SuppressionsWritten = true
			}
			file = updated
		}
	}
	result.Diagnostics = append(result.Diagnostics, unknownRules...)
	if run.lock != nil {
		limited, failures := suppressions.Limit(file, run.lock.Suppressions)
		for _, failure := range failures {
			result.Diagnostics = append(result.Diagnostics, errorDiagnostic(failure.RuleID, "integrity", 0, run.snapshotVersion, run.backendID, fmt.Sprintf("%s %s; review the change and run lintai lock", filepath.Base(state.path), failure.Message)))
		}
		file = limited
	}
	applied := suppressions.Apply(file, result.Diagnostics, scope)
	result.Diagnostics = applied.Reported
	result.Suppressed = applied.Suppressed
	result.UnusedSuppressions = applied.Unused
	if options.Explain {
		explainExceeded(file, applied.Exceeded, result.Diagnostics, filepath.Base(state.path))
	}
	if !options.PassOnUnprunedSuppressions {
		for _, unused := range applied.Unused {
			result.Diagnostics = append(result.Diagnostics, unusedSuppressionDiagnostic(filepath.Base(state.path), unused, run.snapshotVersion, run.backendID))
		}
	}
	sortDiagnostics(result.Diagnostics)
	return nil
}

// blockingRuleErrors counts the rule errors that stop an update of the
// suppressions file, so that a rule that could not run does not lose its
// suppressions. For --suppress-all and --prune-suppressions every rule error
// counts. For --suppress-rule a rule error counts when it is for one of the
// named rules or when its rule id is not the id of a loaded rule, for
// example a rule file that did not bundle.
func blockingRuleErrors(options Options, items []diagnostics.Diagnostic, known map[string]struct{}) int {
	selected := make(map[string]struct{}, len(options.SuppressRules))
	for _, ruleID := range options.SuppressRules {
		selected[ruleID] = struct{}{}
	}
	count := 0
	for _, item := range items {
		if item.DiagnosticKind != diagnostics.KindRuleExecutionError {
			continue
		}
		if len(selected) == 0 {
			count++
			continue
		}
		_, isSelected := selected[item.RuleID]
		_, isKnown := known[item.RuleID]
		if isSelected || !isKnown {
			count++
		}
	}
	return count
}

// unknownSuppressRules reports each --suppress-rule id that names no loaded
// rule, so that a misspelled id does not record nothing without notice.
func unknownSuppressRules(options Options, run runInfo) []diagnostics.Diagnostic {
	seen := make(map[string]struct{})
	ids := make([]string, 0)
	for _, ruleID := range options.SuppressRules {
		if _, ok := run.known[ruleID]; ok {
			continue
		}
		if _, ok := seen[ruleID]; ok {
			continue
		}
		seen[ruleID] = struct{}{}
		ids = append(ids, ruleID)
	}
	sort.Strings(ids)
	result := make([]diagnostics.Diagnostic, 0, len(ids))
	for _, ruleID := range ids {
		result = append(result, errorDiagnostic(ruleID, "config", 0, run.snapshotVersion, run.backendID, fmt.Sprintf("--suppress-rule names rule %q, but no loaded rule has this id", ruleID)))
	}
	return result
}

// unusedSuppressionDiagnostic reports an entry, or an entity of an entry,
// that allows more violations than the run found. Its location is line 1 of
// the file of the entry, so that the linter plugins can show it in that
// file.
func unusedSuppressionDiagnostic(fileName string, unused suppressions.Unused, snapshotVersion, backendID string) diagnostics.Diagnostic {
	subject := fmt.Sprintf("rule %s in %s", unused.RuleID, unused.File)
	var identity *diagnostics.EntityIdentity
	if unused.Entity != "" {
		subject += fmt.Sprintf(" for entity %s", unused.Entity)
		identity = &diagnostics.EntityIdentity{SemanticKey: unused.Entity}
	}
	return diagnostics.Diagnostic{
		RuleID:         unused.RuleID,
		AssertionID:    "suppression",
		DiagnosticKind: diagnostics.KindUnusedSuppression,
		Severity:       diagnostics.SeverityError,
		Message:        fmt.Sprintf("%s suppresses %d violations of %s, but the run found %d. Run lintai --prune-suppressions to lower the count, or pass --pass-on-unpruned-suppressions to ignore unused suppressions.", fileName, unused.Allowed, subject, unused.Found),
		SourceLocation: &diagnostics.SourceLocation{File: unused.File, StartLine: 1, StartColumn: 1, EndLine: 1, EndColumn: 1},
		EntityIdentity: identity,
		Provenance: diagnostics.Provenance{
			SnapshotVersion: snapshotVersion,
			BackendID:       backendID,
		},
	}
}

// explainExceeded adds a line to the explanation of each reported violation
// that a suppressions entry did not suppress because the run found more
// violations than the entry allows.
func explainExceeded(file suppressions.File, exceeded []suppressions.Exceeded, items []diagnostics.Diagnostic, fileName string) {
	type key struct{ file, rule, entity string }
	byKey := make(map[key]suppressions.Exceeded, len(exceeded))
	for _, item := range exceeded {
		byKey[key{item.File, item.RuleID, item.Entity}] = item
	}
	for index := range items {
		item := &items[index]
		if !suppressions.IsCandidate(*item) {
			continue
		}
		filePath := suppressions.NormalizePath(item.SourceLocation.File)
		entry, ok := file[filePath][item.RuleID]
		if !ok {
			continue
		}
		lookup := key{filePath, item.RuleID, ""}
		if len(entry.Entities) > 0 && item.EntityIdentity != nil {
			lookup.entity = item.EntityIdentity.SemanticKey
		}
		found, ok := byKey[lookup]
		if !ok {
			continue
		}
		var line string
		switch {
		case found.Entity == "" && len(entry.Entities) == 0:
			line = fmt.Sprintf("suppressions: %s allows %d violations of rule %s in %s, and the run found %d, so all of them are reported", fileName, found.Allowed, found.RuleID, found.File, found.Found)
		case found.Allowed == 0:
			line = fmt.Sprintf("suppressions: %s does not list entity %q for rule %s in %s, so its violations are reported", fileName, found.Entity, found.RuleID, found.File)
		default:
			line = fmt.Sprintf("suppressions: %s allows %d violations of rule %s in %s for entity %s, and the run found %d, so all of them are reported", fileName, found.Allowed, found.RuleID, found.File, found.Entity, found.Found)
		}
		item.Explanation = append(item.Explanation, line)
	}
}
