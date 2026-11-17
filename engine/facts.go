package engine

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/bigbizze/lintai/backend"
	"github.com/bigbizze/lintai/facts"
)

// Facts explicitly collects and formats the selected base relations without
// preparing or executing rules. An empty selection means all base relations.
func (e *Session) Facts(ctx context.Context, names []string) (string, error) {
	e.checkMu.Lock()
	defer e.checkMu.Unlock()
	ctx, finish, err := e.beginCheck(ctx)
	if err != nil {
		return "", err
	}
	defer finish()
	catalog, err := e.describeProducers()
	if err != nil {
		return "", err
	}
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
	items := make([]facts.Fact, 0)
	for _, entry := range catalog.entries {
		requested := make([]facts.Predicate, 0)
		for _, relation := range entry.description.BaseRelations {
			if _, ok := selected[relation.Predicate.Name]; len(names) == 0 || ok {
				requested = append(requested, relation.Predicate)
			}
		}
		if len(requested) == 0 {
			continue
		}
		batch, err := entry.producer.Collect(ctx, backend.CollectionRequest{RepoRoot: e.defaults.RepoRoot, WorkspaceRoot: e.defaults.WorkspaceRoot, Predicates: requested})
		if err != nil {
			return "", fmt.Errorf("collect producer %q: %w", entry.description.Namespace, err)
		}
		validated, err := facts.ValidateBatch(entry.description, requested, batch)
		if err != nil {
			return "", fmt.Errorf("producer %q returned an invalid batch: %w", entry.description.Namespace, err)
		}
		items = append(items, validated.Facts()...)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	sort.Strings(allowed)
	formatted, err := facts.FormatFacts(items, allowed, names)
	return strings.TrimSpace(formatted), err
}
