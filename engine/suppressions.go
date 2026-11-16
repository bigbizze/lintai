package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strconv"

	"github.com/bigbizze/lintai/diagnostics"
	"github.com/bigbizze/lintai/facts"
	"github.com/bigbizze/lintai/internal/atomicfile"
	"github.com/bigbizze/lintai/suppressions"
)

type assertionSchema struct {
	arity   int
	columns []string
}
type runInfo struct {
	lock                        *RulesLock
	files                       map[string]struct{}
	evaluated, completed, known map[string]struct{}
	coverage                    map[string]map[string]struct{}
	assertions                  map[string]map[string]assertionSchema
	complete                    bool
	snapshotVersion             string
	producerNames               []string
}
type suppressionsState struct {
	path     string
	file     suppressions.File
	snapshot atomicfile.Snapshot
}
type suppressionWrite struct {
	path             string
	snapshot         atomicfile.Snapshot
	data             []byte
	approvalPath     string
	approvalSnapshot atomicfile.Snapshot
}

func validateSuppressionsOptions(options Options) error {
	switch {
	case options.SuppressAll && len(options.SuppressRules) > 0:
		return fmt.Errorf("--suppress-all and --suppress-rule cannot be used together")
	case (options.SuppressAll || len(options.SuppressRules) > 0) && options.PruneSuppressions:
		return fmt.Errorf("recording and --prune-suppressions cannot be used together")
	}
	return nil
}
func loadSuppressions(options Options) (suppressionsState, error) {
	if err := validateSuppressionsOptions(options); err != nil {
		return suppressionsState{}, err
	}
	path := suppressions.ResolvePath(options.WorkspaceRoot, options.SuppressionsLocation)
	snapshot, err := atomicfile.Read(path)
	if err != nil {
		return suppressionsState{}, err
	}
	if !snapshot.Exists {
		if options.SuppressionsLocation != "" && !options.SuppressAll && len(options.SuppressRules) == 0 {
			return suppressionsState{}, fmt.Errorf("the suppressions file %s does not exist", path)
		}
		return suppressionsState{path: path, file: suppressions.File{}, snapshot: snapshot}, nil
	}
	file, err := suppressions.Parse(path, snapshot.Data)
	if err != nil {
		return suppressionsState{}, err
	}
	return suppressionsState{path: path, file: file, snapshot: snapshot}, nil
}
func typedReason(v facts.Value) ([2]string, error) {
	switch v.Kind {
	case facts.StringKind:
		return [2]string{"s", v.String}, nil
	case facts.NameKind:
		return [2]string{"n", v.String}, nil
	case facts.NumberKind:
		return [2]string{"i", strconv.FormatInt(v.Number, 10)}, nil
	case facts.FloatKind:
		if math.IsNaN(v.Float) || math.IsInf(v.Float, 0) {
			return [2]string{}, fmt.Errorf("non-finite suppression reason")
		}
		f := v.Float
		if f == 0 {
			f = 0
		}
		return [2]string{"f", fmt.Sprintf("%016x", math.Float64bits(f))}, nil
	}
	return [2]string{}, fmt.Errorf("unsupported suppression reason")
}
func reasonsFor(item violation, names []string) ([]suppressions.Reason, error) {
	reasons := make([]suppressions.Reason, 0, len(names))
	for _, name := range names {
		found := -1
		for i, column := range item.columns {
			if column == name {
				if found >= 0 {
					return nil, fmt.Errorf("ambiguous column %q", name)
				}
				found = i
			}
		}
		if found <= 0 {
			return nil, fmt.Errorf("column %q does not resolve to a distinct non-anchor column", name)
		}
		value, err := typedReason(item.typedRow[found])
		if err != nil {
			return nil, err
		}
		reasons = append(reasons, suppressions.Reason{Name: name, Value: value})
	}
	return reasons, nil
}
func savedColumns(entry suppressions.Entry) []string {
	names := make([]string, 0)
	if len(entry.Groups) > 0 {
		for _, r := range entry.Groups[0].Reasons {
			names = append(names, r.Name)
		}
	}
	return names
}
func validatePolicies(file suppressions.File, run runInfo) error {
	for _, entry := range file {
		for _, group := range entry.Groups {
			for _, reason := range group.Reasons {
				if err := suppressions.ValidateReason(reason.Value); err != nil {
					return &suppressions.UsageError{Path: entry.File, Message: fmt.Sprintf("suppression_policy_mismatch: rule %s assertion %s/%d: %v", entry.Rule, entry.Assertion.Name, entry.Assertion.Arity, err)}
				}
			}
		}
		schema, ok := run.assertions[entry.Rule][entry.Assertion.Name]
		if !ok || schema.arity != entry.Assertion.Arity {
			continue
		}
		for _, name := range savedColumns(entry) {
			count, position := 0, -1
			for i, column := range schema.columns {
				if column == name {
					count++
					position = i
				}
			}
			if count != 1 || position == 0 {
				return &suppressions.UsageError{Path: entry.File, Message: fmt.Sprintf("suppression_policy_mismatch: rule %s assertion %s/%d: reason column %q does not resolve to a distinct non-anchor column", entry.Rule, entry.Assertion.Name, entry.Assertion.Arity, name)}
			}
		}
	}
	return nil
}

type groupFindings struct {
	entry   suppressions.Entry
	group   suppressions.Group
	indexes []int
}

func diagnosticKey(item diagnostics.Diagnostic) string {
	data, _ := json.Marshal(item)
	return string(data)
}
func collectGroups(file suppressions.File, result Result, evidence executionEvidence, useSaved bool) (map[string]*groupFindings, error) {
	saved := map[string]suppressions.Entry{}
	for _, entry := range file {
		saved[suppressions.EntryID(entry)] = entry
	}
	indexes := map[string][]int{}
	for i, item := range result.Diagnostics {
		if item.DiagnosticKind == diagnostics.KindArchitectureViolation && item.Severity == diagnostics.SeverityError && item.SourceLocation != nil {
			key := diagnosticKey(item)
			indexes[key] = append(indexes[key], i)
		}
	}
	groups := map[string]*groupFindings{}
	for _, item := range evidence.findings {
		diag := item.diagnostic
		// Severity is presentation policy and may have changed during reuse.
		diag.Severity = diagnostics.SeverityError
		key := diagnosticKey(diag)
		available := indexes[key]
		if len(available) == 0 {
			continue
		}
		index := available[0]
		indexes[key] = available[1:]
		entry := suppressions.Entry{File: suppressions.NormalizePath(item.location.File), Rule: item.ruleID, Assertion: suppressions.Assertion{Name: item.predicate, Arity: len(item.typedRow)}}
		by := item.by
		if existing, ok := saved[suppressions.EntryID(entry)]; ok && useSaved {
			by = savedColumns(existing)
		}
		reasons, err := reasonsFor(item, by)
		if err != nil {
			return nil, &suppressions.UsageError{Path: entry.File, Message: fmt.Sprintf("suppression_policy_mismatch: %s/%s: %v", entry.Rule, entry.Assertion.Name, err)}
		}
		support, ok := evidence.anchors[item.anchor]
		if !ok {
			return nil, fmt.Errorf("missing anchor support for %q", item.anchor)
		}
		group := suppressions.Group{Namespace: evidence.anchorOwners[item.anchor], Reasons: reasons, Description: support.Description, Count: 1}
		if support.Tracking != nil {
			group.Key = &suppressions.Key{Scheme: support.Tracking.Scheme, Data: support.Tracking.Data}
		} else {
			group.Description = "untracked anchors"
		}
		id, err := suppressions.GroupID(entry, group)
		if err != nil {
			return nil, err
		}
		current := groups[id]
		if current == nil {
			current = &groupFindings{entry: entry, group: group}
			groups[id] = current
		} else if group.Description < current.group.Description {
			current.group.Description = group.Description
		}
		current.indexes = append(current.indexes, index)
	}
	return groups, nil
}
func selectedRule(options Options, rule string) bool {
	if options.SuppressAll {
		return true
	}
	for _, id := range options.SuppressRules {
		if id == rule {
			return true
		}
	}
	return false
}
func covered(run runInfo, entry suppressions.Entry) bool {
	if _, ok := run.evaluated[entry.Rule]; !ok {
		return false
	}
	if _, ok := run.coverage[entry.Rule][entry.File]; !ok {
		return false
	}
	schema, ok := run.assertions[entry.Rule][entry.Assertion.Name]
	return ok && schema.arity == entry.Assertion.Arity
}
func applySuppressions(options Options, state suppressionsState, run runInfo, evidence executionEvidence, result *Result) (*suppressionWrite, error) {
	result.SuppressionsPath = state.path
	unknown := unknownSuppressRules(options, run)
	result.Diagnostics = append(result.Diagnostics, unknown...)
	writing := options.SuppressAll || len(options.SuppressRules) > 0 || options.PruneSuppressions
	blocked := len(unknown) > 0 || blockingRuleErrors(options, result.Diagnostics, run.known) > 0
	file := state.file
	if writing && blocked {
		result.Warnings = append(result.Warnings, fmt.Sprintf("lintai did not update %s because selected rules failed", state.path))
	}
	if writing && !blocked && !options.PruneSuppressions {
		raw, err := collectGroups(nil, *result, evidence, false)
		if err != nil {
			return nil, err
		}
		replacements := map[string]bool{}
		entries := map[string]*suppressions.Entry{}
		for _, group := range raw {
			if !selectedRule(options, group.entry.Rule) {
				continue
			}
			id := suppressions.EntryID(group.entry)
			replacements[id] = true
			if entries[id] == nil {
				entry := group.entry
				entries[id] = &entry
			}
			group.group.Count = len(group.indexes)
			entries[id].Groups = append(entries[id].Groups, group.group)
		}
		file = suppressions.File{}
		for _, entry := range state.file {
			if !replacements[suppressions.EntryID(entry)] {
				file = append(file, entry)
			}
		}
		for _, entry := range entries {
			file = append(file, *entry)
		}
	}
	if err := validatePolicies(file, run); err != nil {
		return nil, err
	}
	groups, err := collectGroups(file, *result, evidence, true)
	if err != nil {
		return nil, err
	}
	if writing && !blocked && options.PruneSuppressions {
		pruned := suppressions.File{}
		for _, entry := range file {
			copyEntry := entry
			copyEntry.Groups = nil
			for _, group := range entry.Groups {
				id, _ := suppressions.GroupID(entry, group)
				found := 0
				if current := groups[id]; current != nil {
					found = len(current.indexes)
				}
				if covered(run, entry) && found < group.Count {
					group.Count = found
				}
				if group.Count > 0 {
					copyEntry.Groups = append(copyEntry.Groups, group)
				}
			}
			if len(copyEntry.Groups) > 0 {
				pruned = append(pruned, copyEntry)
			}
		}
		file = pruned
	}
	if err := suppressions.Validate(file); err != nil {
		return nil, err
	}
	if run.lock != nil {
		limited, failures := suppressions.Limit(file, run.lock.Suppressions)
		for _, failure := range failures {
			result.Diagnostics = append(result.Diagnostics, errorDiagnostic(failure.RuleID, "integrity", 0, run.snapshotVersion, run.producerNames, fmt.Sprintf("%s %s; review and run lintai lock", filepath.Base(state.path), failure.Message)))
		}
		if len(failures) > 0 {
			blocked = true
		}
		file = limited
	}
	var write *suppressionWrite
	if writing && !blocked && (state.snapshot.Exists || !options.PruneSuppressions) {
		data, err := suppressions.Encode(file)
		if err != nil {
			return nil, err
		}
		write = &suppressionWrite{path: state.path, snapshot: state.snapshot, data: data}
		if run.lock != nil {
			write.approvalPath = run.lock.target
			write.approvalSnapshot = run.lock.targetSnapshot
		}
	}
	suppressed := map[int]bool{}
	for _, entry := range file {
		for _, group := range entry.Groups {
			id, _ := suppressions.GroupID(entry, group)
			current := groups[id]
			found := 0
			if current != nil {
				found = len(current.indexes)
			}
			if found <= group.Count {
				if current != nil {
					for _, index := range current.indexes {
						suppressed[index] = true
					}
				}
				if found < group.Count && covered(run, entry) {
					result.UnusedSuppressions = append(result.UnusedSuppressions, suppressions.Unused{File: entry.File, RuleID: entry.Rule, Assertion: entry.Assertion, Description: group.Description, Allowed: group.Count, Found: found})
				}
			}
			if found > group.Count && options.Explain && current != nil {
				for _, index := range current.indexes {
					result.Diagnostics[index].Explanation = append(result.Diagnostics[index].Explanation, fmt.Sprintf("suppressions: %s allows %d, found %d; all findings are reported", filepath.Base(state.path), group.Count, found))
				}
			}
		}
	}
	reported := make([]diagnostics.Diagnostic, 0, len(result.Diagnostics))
	for i, item := range result.Diagnostics {
		if suppressed[i] {
			result.Suppressed = append(result.Suppressed, item)
		} else {
			reported = append(reported, item)
		}
	}
	result.Diagnostics = reported
	sort.Slice(result.UnusedSuppressions, func(i, j int) bool {
		a, b := result.UnusedSuppressions[i], result.UnusedSuppressions[j]
		return fmt.Sprint(a.File, a.RuleID, a.Assertion.Name, a.Description) < fmt.Sprint(b.File, b.RuleID, b.Assertion.Name, b.Description)
	})
	if !options.PassOnUnprunedSuppressions {
		for _, unused := range result.UnusedSuppressions {
			result.Diagnostics = append(result.Diagnostics, unusedSuppressionDiagnostic(filepath.Base(state.path), unused, run.snapshotVersion, run.producerNames))
		}
	}
	sortDiagnostics(result.Diagnostics)
	return write, nil
}
func blockingRuleErrors(options Options, items []diagnostics.Diagnostic, known map[string]struct{}) int {
	selected := map[string]bool{}
	for _, id := range options.SuppressRules {
		selected[id] = true
	}
	count := 0
	for _, item := range items {
		if item.DiagnosticKind != diagnostics.KindRuleExecutionError {
			continue
		}
		_, isKnown := known[item.RuleID]
		if len(selected) == 0 || selected[item.RuleID] || !isKnown {
			count++
		}
	}
	return count
}
func unknownSuppressRules(options Options, run runInfo) []diagnostics.Diagnostic {
	if !run.complete {
		return nil
	}
	seen := map[string]bool{}
	var out []diagnostics.Diagnostic
	for _, id := range options.SuppressRules {
		if _, ok := run.known[id]; ok || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, errorDiagnostic(id, "config", 0, run.snapshotVersion, run.producerNames, fmt.Sprintf("--suppress-rule names unknown rule %q", id)))
	}
	return out
}
func unusedSuppressionDiagnostic(fileName string, unused suppressions.Unused, snapshotVersion string, producers []string) diagnostics.Diagnostic {
	return diagnostics.Diagnostic{RuleID: unused.RuleID, AssertionID: "suppression", DiagnosticKind: diagnostics.KindUnusedSuppression, Severity: diagnostics.SeverityError, Message: fmt.Sprintf("%s allows %d for %s/%s in %s (%s), but found %d", fileName, unused.Allowed, unused.RuleID, unused.Assertion.Name, unused.File, unused.Description, unused.Found), SourceLocation: &diagnostics.SourceLocation{File: unused.File, StartLine: 1, StartColumn: 1, EndLine: 1, EndColumn: 1}, Provenance: diagnostics.Provenance{SnapshotVersion: snapshotVersion, ProducerNamespaces: append([]string(nil), producers...)}}
}

// commitSuppression makes the guarded rename the cancellation commit boundary.
func commitSuppression(ctx context.Context, write *suppressionWrite) error {
	return atomicfile.Write(ctx, write.path, write.snapshot, write.data, "suppression_changed", write.approvalPath, write.approvalSnapshot, "rules_lock_changed")
}
