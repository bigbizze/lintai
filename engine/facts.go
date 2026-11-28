package engine

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/bigbizze/lintai/facts"
	"github.com/bigbizze/lintai/observe"
)

// Facts explicitly collects and formats the selected base relations without
// preparing or executing rules. An empty selection means all base relations.
func (e *Session) Facts(ctx context.Context, names []string) (string, error) {
	ctx, finish, err := e.beginCheck(ctx)
	if err != nil {
		return "", err
	}
	defer finish()
	catalog := e.catalog
	allowed := make([]string, 0)
	known := make(map[string]struct{})
	for _, entry := range catalog.entries {
		for _, relation := range entry.description.BaseRelations {
			allowed = append(allowed, relation.Predicate.Name)
			known[relation.Predicate.Name] = struct{}{}
		}
	}
	selected := make(map[string]struct{}, len(names))
	for _, name := range names {
		if _, ok := known[name]; !ok {
			return "", fmt.Errorf("unknown base relation %q", name)
		}
		selected[name] = struct{}{}
	}
	requested := make(map[string]map[facts.Predicate]struct{})
	for _, entry := range catalog.entries {
		for _, relation := range entry.description.BaseRelations {
			if _, ok := selected[relation.Predicate.Name]; len(names) == 0 || ok {
				if requested[entry.description.Namespace] == nil {
					requested[entry.description.Namespace] = make(map[facts.Predicate]struct{})
				}
				requested[entry.description.Namespace][relation.Predicate] = struct{}{}
			}
		}
	}
	root, _, err := e.operationRoot()
	if err != nil {
		return "", err
	}
	assembled, err := catalog.collect(ctx, root, requested, nil, observe.Full)
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	sort.Strings(allowed)
	formatted, err := facts.FormatFacts(assembled.base, allowed, names)
	return strings.TrimSpace(formatted), err
}
