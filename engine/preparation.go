package engine

import (
	"context"
	"fmt"
	"path/filepath"

	"codeberg.org/TauCeti/mangle-go/factstore"
	"github.com/bigbizze/lintai/bundle"
	"github.com/bigbizze/lintai/diagnostics"
	"github.com/bigbizze/lintai/facts"
	"github.com/bigbizze/lintai/internal/canonical"
	"github.com/bigbizze/lintai/runtime"
)

func (rule *ruleRecord) addError(id, phase string, version int, snapshot string, producers []string, message string) {
	rule.diagnostics = append(rule.diagnostics, errorDiagnostic(id, phase, version, snapshot, producers, message))
}

// The hash barrier precedes even pure metadata loading. A missing locked file
// belongs to the inventory, including when discovery found no rule records.
func preMetadataIntegrity(catalog *ruleCatalog, lock *RulesLock, workspace, snapshot string) ([]diagnostics.Diagnostic, bool) {
	if lock == nil {
		return nil, true
	}
	complete := true
	seen := make(map[string]struct{}, len(catalog.rules))
	for _, rule := range catalog.rules {
		key := lockKey(workspace, rule.path)
		seen[key] = struct{}{}
		if !rule.bundled {
			continue
		}
		if approved, ok := lock.Rules[key]; ok && approved.Hash != rule.artifact.Hash {
			message := fmt.Sprintf("rule %s bundle changed since the rules lock was written (locked %s, now %s); review the change and run lintai lock", key, approved.Hash, rule.artifact.Hash)
			rule.addError(key, "integrity", 0, snapshot, nil, message)
			rule.disposition = ruleIntegrityRejected
			complete = false
		}
	}
	var inventory []diagnostics.Diagnostic
	for key := range lock.Rules {
		if _, ok := seen[key]; !ok {
			message := fmt.Sprintf("rule %s is in the rules lock but was not found; review the removal and run lintai lock", key)
			inventory = append(inventory, errorDiagnostic(key, "integrity", 0, snapshot, nil, message))
			complete = false
		}
	}
	return inventory, complete
}

// Unapproved helpers are harmless, but a malformed default export can still
// be unapproved and must retain both its integrity and metadata diagnostics.
func postMetadataIntegrity(catalog *ruleCatalog, lock *RulesLock, workspace, snapshot string) bool {
	if lock == nil {
		return true
	}
	complete := true
	for _, rule := range catalog.rules {
		if !rule.metadataRead || !rule.isRule {
			continue
		}
		key := lockKey(workspace, rule.path)
		approved, ok := lock.Rules[key]
		message := ""
		if !ok {
			message = fmt.Sprintf("rule %s is not in the rules lock; review it and run lintai lock", key)
		} else if rule.loadError == nil && (approved.ID != rule.metadata.ID || approved.Version != rule.metadata.Version) {
			message = fmt.Sprintf("rule %s metadata does not match the rules lock", key)
		}
		if message != "" {
			rule.addError(key, "integrity", 0, snapshot, nil, message)
			rule.disposition = ruleIntegrityRejected
			complete = false
		}
	}
	return complete
}

// Duplicate lookup includes valid IDs from malformed and unapproved records.
// A malformed record reports its metadata error, while its peers report the
// duplicate; duplicates do not themselves make the inventory incomplete.
func classifyRuleIDs(catalog *ruleCatalog, workspace, snapshot string) (map[string]struct{}, int) {
	known := make(map[string]struct{})
	byID := make(map[string][]*ruleRecord)
	loaded := 0
	for _, rule := range catalog.rules {
		if !rule.metadataRead {
			continue
		}
		if rule.isRule {
			loaded++
		}
		id := rule.metadata.ID
		if id != "" {
			known[id] = struct{}{}
			if runtime.ValidateRuleID(id) == nil {
				byID[id] = append(byID[id], rule)
			}
		}
	}
	for _, rule := range catalog.rules {
		peers := byID[rule.metadata.ID]
		if len(peers) < 2 || rule.loadError != nil {
			continue
		}
		keys := make([]string, len(peers))
		for i, peer := range peers {
			keys[i] = lockKey(workspace, peer.path)
		}
		rule.addError(rule.metadata.ID, "config", rule.metadata.Version, snapshot, nil, duplicateIDMessage(rule.metadata.ID, keys))
		rule.disposition = ruleDuplicate
	}
	return known, loaded
}

// Eligible config/setup work is sent in a single Node invocation. The maps
// below are temporary adapters for the bundle API; the records own outcomes.
func prepareRuleConfigs(ctx context.Context, catalog *ruleCatalog, options Options, sandbox bool, locked bool, snapshot string, run *runInfo) {
	requests := make(map[string]bundle.Artifacts)
	configOnly := make(map[string]struct{})
	for _, rule := range catalog.rules {
		if rule.loadError != nil {
			run.complete = false
			rule.addError(ruleIDOrPath(rule.metadata.ID, rule.path), "setup", rule.metadata.Version, snapshot, nil, rule.loadError.Error())
			rule.disposition = ruleMalformed
			continue
		}
		if rule.disposition != rulePending || !rule.isRule {
			continue
		}
		rule.severity = severityOf(options, rule.metadata.ID)
		if rule.severity == diagnostics.SeverityOff {
			rule.disposition = ruleDisabled
			continue
		}
		if !rule.metadata.HasSetup && !rule.metadata.HasConfig {
			env := options.Env
			if env == nil {
				env = map[string]any{}
			}
			rule.preparation = bundle.PrepareResult{RuleID: rule.metadata.ID, RuleVersion: rule.metadata.Version, Env: env, Setup: map[string]any{}}
			rule.disposition = rulePrepared
			continue
		}
		artifact, err := catalog.prepareArtifact(rule)
		if err != nil {
			rule.addError(rule.metadata.ID, "setup", rule.metadata.Version, snapshot, nil, err.Error())
			rule.disposition = ruleFailed
			continue
		}
		requests[rule.path] = artifact
		if !rule.metadata.HasSetup {
			configOnly[rule.path] = struct{}{}
		}
		rule.disposition = rulePreparing
	}
	prepared, failures, err := bundle.PrepareAll(ctx, assetRootOf(options), options.RepoRoot, options.WorkspaceRoot, requests, options.Env, bundle.PrepareOptions{
		Sandbox: sandbox, RestrictRequire: locked, SkipSetup: configOnly,
	})
	for _, rule := range catalog.rules {
		if rule.disposition != rulePreparing {
			continue
		}
		if err != nil {
			rule.addError(rule.metadata.ID, "setup", rule.metadata.Version, snapshot, nil, err.Error())
			rule.disposition = ruleFailed
			continue
		}
		if failure, ok := failures[rule.path]; ok {
			rule.addError(ruleIDOrPath(rule.metadata.ID, rule.path), "setup", rule.metadata.Version, snapshot, nil, failure.Message)
		}
		if output, ok := prepared[rule.path]; ok {
			rule.preparation = output
			rule.disposition = rulePrepared
		} else {
			rule.disposition = ruleFailed
		}
	}
}

// Every actual check loads fresh runtimes and reruns program/fact generation.
func (e *Session) generateRulePrograms(catalog *ruleCatalog, snapshot string) {
	for _, rule := range catalog.rules {
		if rule.disposition != rulePrepared {
			continue
		}
		if _, err := canonical.Marshal(rule.preparation.Env); err != nil {
			rule.addError(rule.metadata.ID, "setup", rule.metadata.Version, snapshot, nil, err.Error())
			rule.disposition = ruleFailed
			continue
		}
		if _, err := canonical.Marshal(rule.preparation.Setup); err != nil {
			rule.addError(rule.metadata.ID, "setup", rule.metadata.Version, snapshot, nil, err.Error())
			rule.disposition = ruleFailed
			continue
		}
		e.stats.gojaRuntimes.Add(1)
		loaded, err := runtime.LoadPureBundle(rule.retained.pureCode)
		if err != nil {
			rule.addError(rule.metadata.ID, "program", rule.metadata.Version, snapshot, nil, err.Error())
			rule.disposition = ruleFailed
			continue
		}
		if loaded.RuleID() != rule.metadata.ID || loaded.RuleVersion() != rule.metadata.Version {
			rule.addError(rule.metadata.ID, "program", rule.metadata.Version, snapshot, nil, "rule metadata changed between catalog and execution load")
			rule.disposition = ruleFailed
			continue
		}
		e.stats.programBuilds.Add(1)
		output, err := loaded.Program(rule.preparation.Env, rule.preparation.Setup)
		if err != nil {
			rule.addError(rule.metadata.ID, "program", rule.metadata.Version, snapshot, nil, err.Error())
			rule.disposition = ruleFailed
			continue
		}
		rule.loaded, rule.output, rule.disposition = loaded, output, ruleGenerated
	}
}

func (e *Session) prepareRulePlans(catalog *ruleCatalog, producers producerCatalog, snapshot string) {
	for _, rule := range catalog.rules {
		if rule.disposition != ruleGenerated {
			continue
		}
		key, keyErr := generatedRulePlanKey(producers.identity, rule.output, rule.loaded.Report(), rule.loaded.ExpectNonEmpty(), rule.loaded.SuppressionDeclarations())
		entry := rule.retained
		var plan *rulePlan
		if keyErr == nil && key != "" && entry.planKey == key {
			plan = entry.plan
		}
		var perr *phaseError
		if plan != nil {
			var policies []suppressionPolicy
			policies, perr = validateSuppressionPolicies(rule.loaded.SuppressionDeclarations(), plan.reportDecls)
			if perr == nil {
				var extraFacts factstore.ReadOnlyFactStore
				extraFacts, perr = prepareExtraFacts(rule.output, producers.namespaces)
				if perr == nil {
					plan = planWithExtraFacts(plan, extraFacts)
					plan.suppressionRules = policies
				}
			}
		} else {
			e.stats.mangleCompiles.Add(1)
			plan, perr = prepareProgram(producers.sources, producers.schemaDecls, producers.namespaces,
				rule.output, rule.loaded.Report(), rule.loaded.ExpectNonEmpty(), rule.loaded.SuppressionDeclarations())
			if perr == nil && keyErr == nil && key != "" {
				template := *plan
				template.ruleFacts = nil
				template.suppressionRules = nil
				entry.planKey, entry.plan = key, &template
			}
		}
		if perr != nil {
			rule.addError(rule.metadata.ID, perr.phase, rule.metadata.Version, snapshot, producers.namespaces, perr.Error())
			rule.disposition = ruleFailed
			continue
		}
		rule.plan, rule.disposition = plan, rulePlanned
	}
}

func ruleRelationDemand(catalog *ruleCatalog, producers producerCatalog) ([]*preparedRuleJob, map[string]map[facts.Predicate]struct{}) {
	var jobs []*preparedRuleJob
	requested := make(map[string]map[facts.Predicate]struct{})
	for _, rule := range catalog.rules {
		if rule.disposition != rulePlanned {
			continue
		}
		for _, entry := range producers.entries {
			for _, relation := range entry.description.BaseRelations {
				if _, needed := rule.plan.required[relation.Predicate.Name]; !needed {
					continue
				}
				if requested[entry.description.Namespace] == nil {
					requested[entry.description.Namespace] = make(map[facts.Predicate]struct{})
				}
				requested[entry.description.Namespace][relation.Predicate] = struct{}{}
			}
		}
		jobs = append(jobs, &preparedRuleJob{record: rule, ruleID: rule.metadata.ID, version: rule.metadata.Version,
			severity: rule.severity, loaded: rule.loaded, plan: rule.plan})
	}
	return jobs, requested
}

func catalogDiagnostics(catalog *ruleCatalog, inventory []diagnostics.Diagnostic) []diagnostics.Diagnostic {
	items := append([]diagnostics.Diagnostic{}, inventory...)
	for _, rule := range catalog.rules {
		items = append(items, rule.diagnostics...)
	}
	return items
}

func bundleDiagnostic(rule *ruleRecord, err error, snapshot string) {
	rule.addError(filepath.Base(rule.path), "bundle", 0, snapshot, nil, err.Error())
}
