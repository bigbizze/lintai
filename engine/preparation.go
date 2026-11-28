package engine

import (
	"fmt"
	"strings"

	"github.com/bigbizze/lintai/diagnostics"
	"github.com/bigbizze/lintai/facts"
	"github.com/bigbizze/lintai/runtime"
)

func (rule *ruleRecord) addError(id, phase string, version int, message string) {
	rule.diagnostics = append(rule.diagnostics, errorDiagnostic(id, phase, version, "", nil, message))
}

func classifyRuleIDs(catalog *ruleCatalog) (map[string]struct{}, int) {
	known := make(map[string]struct{})
	byID := make(map[string][]*ruleRecord)
	loaded := 0
	for _, rule := range catalog.rules {
		if rule.metadata.ID != "" {
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
		if len(peers) < 2 {
			continue
		}
		keys := make([]string, len(peers))
		for i, peer := range peers {
			keys[i] = peer.path
		}
		rule.addError(rule.metadata.ID, "config", rule.metadata.Version, duplicateIDMessage(rule.metadata.ID, keys))
		rule.disposition = ruleDuplicate
	}
	return known, loaded
}

func generateRulePrograms(catalog *ruleCatalog, options Config) {
	for _, rule := range catalog.rules {
		if rule.loadError != nil {
			rule.addError(ruleIDOrPath(rule.metadata.ID, rule.path), "load", rule.metadata.Version, fmt.Sprintf("%s: %v", rule.path, rule.loadError))
			continue
		}
		if rule.disposition != rulePending {
			continue
		}
		rule.severity = severityOf(options, rule.metadata.ID)
		if rule.severity == diagnostics.SeverityOff {
			rule.disposition = ruleDisabled
			continue
		}
		if err := rule.loaded.Configure(options.Rule[rule.metadata.ID].Options); err != nil {
			rule.addError(rule.metadata.ID, "config", rule.metadata.Version, err.Error())
			rule.disposition = ruleFailed
			continue
		}
		output, err := rule.loaded.Program()
		if err != nil {
			rule.addError(rule.metadata.ID, "program", rule.metadata.Version, err.Error())
			rule.disposition = ruleFailed
			continue
		}
		rule.output, rule.disposition = output, ruleGenerated
	}
}

func prepareRulePlans(catalog *ruleCatalog, producers producerCatalog) {
	for _, rule := range catalog.rules {
		if rule.disposition != ruleGenerated {
			continue
		}
		metadata := rule.metadata
		key, keyErr := generatedRulePlanKey(producers.identity, rule.output, metadata.Report, metadata.ExpectNonEmpty, metadata.Suppressions)
		entry := rule.retained
		var plan *rulePlan
		if keyErr == nil && key != "" && entry.planKey == key {
			plan = entry.plan
		}
		var perr *phaseError
		if plan == nil {
			plan, perr = prepareProgram(producers.sources, producers.schemaDecls, producers.namespaces,
				rule.output, metadata.Report, metadata.ExpectNonEmpty, metadata.Suppressions)
			if perr == nil && keyErr == nil && key != "" {
				entry.planKey, entry.plan = key, plan
			}
		}
		if perr != nil {
			rule.addError(rule.metadata.ID, perr.phase, rule.metadata.Version, perr.Error())
			rule.disposition = ruleFailed
			continue
		}
		rule.plan, rule.disposition = plan, rulePlanned
	}
}

func ruleRelationDemand(catalog *ruleCatalog) ([]*ruleRecord, map[string]map[facts.Predicate]struct{}) {
	var jobs []*ruleRecord
	requested := make(map[string]map[facts.Predicate]struct{})
	for _, rule := range catalog.rules {
		if rule.disposition != rulePlanned {
			continue
		}
		// Validated catalog declarations make every base predicate a qualified
		// producer relation with its exact arity.
		for predicate := range rule.plan.basePredicates {
			namespace, _, _ := strings.Cut(predicate.Symbol, ".")
			if requested[namespace] == nil {
				requested[namespace] = make(map[facts.Predicate]struct{})
			}
			requested[namespace][facts.Predicate{Name: predicate.Symbol, Arity: predicate.Arity}] = struct{}{}
		}
		jobs = append(jobs, rule)
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
