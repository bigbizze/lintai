package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bigbizze/lintai/diagnostics"
	"github.com/bigbizze/lintai/internal/rulepack"
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

// RuleLoadError identifies a malformed entry or duplicate semantic ID.
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

type ruleRecord struct {
	path, code  string
	retained    *ruleEntry
	metadata    runtime.RuleMetadata
	loadError   error
	loaded      *runtime.LoadedRule
	output      runtime.ProgramOutput
	plan        *rulePlan
	severity    diagnostics.Severity
	diagnostics []diagnostics.Diagnostic
	disposition ruleDisposition
}

type ruleDisposition uint8

const (
	rulePending ruleDisposition = iota
	ruleDuplicate
	ruleDisabled
	ruleGenerated
	rulePlanned
	ruleCompleted
	ruleFailed
)

type ruleCatalog struct {
	rules        []*ruleRecord
	observations observe.Set
	digest       string
}

type ruleEntry struct {
	planKey string
	plan    *rulePlan
}

func openRuleCatalog(ctx context.Context, workspaceRoot string, session *Session) (*ruleCatalog, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path, err := filepath.Abs(filepath.Join(workspaceRoot, rulepack.Path))
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("rule pack %s is missing; run lintai build-rules", rulepack.Path)
	}
	if err != nil {
		return nil, err
	}
	pack, err := rulepack.Decode(data)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	obs, err := observe.New([]observe.Record{{Kind: observe.Content, Path: path, Value: observe.Value(digest)}})
	if err != nil {
		return nil, err
	}
	catalog := &ruleCatalog{digest: "sha256:" + digest, observations: obs}
	active := make(map[string]*ruleEntry, len(pack.Entries))
	for _, item := range pack.Entries {
		var retained *ruleEntry
		if session != nil {
			retained = session.ruleEntries[item.Entry]
		}
		if retained == nil {
			retained = &ruleEntry{}
		}
		active[item.Entry] = retained
		catalog.rules = append(catalog.rules, &ruleRecord{path: item.Entry, code: item.Code, retained: retained})
	}
	if session != nil {
		session.ruleEntries = active
	}
	return catalog, nil
}

func (catalog *ruleCatalog) loadMetadata(ctx context.Context, retain bool) error {
	for _, entry := range catalog.rules {
		if err := ctx.Err(); err != nil {
			return err
		}
		inspection, err := runtime.Inspect(ctx, entry.code)
		entry.metadata, entry.loadError = inspection.Metadata, err
		if retain {
			entry.loaded = inspection.Rule
		} else if inspection.Rule != nil {
			inspection.Rule.Close()
		}
	}
	return ctx.Err()
}

func (catalog *ruleCatalog) close() {
	for _, rule := range catalog.rules {
		rule.release()
	}
}

// ListRules inspects the published entries without calling rule callbacks.
func ListRules(ctx context.Context, workspaceRoot string) (RuleList, error) {
	catalog, err := openRuleCatalog(ctx, workspaceRoot, nil)
	if err != nil {
		return RuleList{}, err
	}
	if err := catalog.loadMetadata(ctx, false); err != nil {
		return RuleList{}, err
	}
	list := RuleList{Rules: []ListedRule{}, Errors: []RuleLoadError{}}
	type foundRule struct {
		ListedRule
		shapeErr error
	}
	found := make([]foundRule, 0, len(catalog.rules))
	for _, entry := range catalog.rules {
		key := entry.path
		if entry.loadError != nil {
			message := entry.loadError.Error()
			if entry.metadata.ID != "" && runtime.ValidateRuleID(entry.metadata.ID) == nil {
				found = append(found, foundRule{ListedRule: ListedRule{ID: entry.metadata.ID, Path: key}, shapeErr: entry.loadError})
			} else {
				list.Errors = append(list.Errors, RuleLoadError{Path: key, Message: message})
			}
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
	return list, ctx.Err()
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
