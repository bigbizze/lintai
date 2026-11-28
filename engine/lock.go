package engine

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/bigbizze/lintai/internal/atomicfile"
	"github.com/bigbizze/lintai/internal/canonical"
	"github.com/bigbizze/lintai/internal/rulepack"
	"github.com/bigbizze/lintai/observe"
	"github.com/bigbizze/lintai/suppressions"
)

const DefaultRulesLockFile = "lintai-rules.lock.json"
const RulesLockFormat = "lintai.rules-lock/5"

type RulesLock struct {
	PackDigest   string
	PolicyDigest string
	Suppressions suppressions.File
	// target and targetSnapshot are the lock file read by a locked check,
	// which guards that check's suppression write.
	target         string
	targetSnapshot atomicfile.Snapshot
	observations   observe.Set
}
type lockJSON struct {
	Format       string          `json:"format"`
	ABI          string          `json:"abi"`
	PackDigest   string          `json:"pack_digest"`
	PolicyDigest string          `json:"policy_digest"`
	Approved     json.RawMessage `json:"approved_suppressions"`
}

func validDigest(s string) bool {
	hash, ok := strings.CutPrefix(s, "sha256:")
	if !ok || len(hash) != 64 || strings.ToLower(hash) != hash {
		return false
	}
	_, err := hex.DecodeString(hash)
	return err == nil
}

func parseRulesLock(path string, data []byte) (RulesLock, error) {
	if !utf8.Valid(data) {
		return RulesLock{}, fmt.Errorf("rules lock %s: invalid UTF-8", path)
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	if err := uniqueObjectKeys(d, true); err != nil {
		return RulesLock{}, fmt.Errorf("rules lock %s: %w", path, err)
	}
	if _, err := d.Token(); err != io.EOF {
		return RulesLock{}, fmt.Errorf("rules lock %s has trailing data", path)
	}
	var raw lockJSON
	d = json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(&raw); err != nil {
		return RulesLock{}, fmt.Errorf("rules lock %s: %w", path, err)
	}
	if raw.Format != RulesLockFormat || raw.ABI != rulepack.ABI || !validDigest(raw.PackDigest) || !validDigest(raw.PolicyDigest) || len(raw.Approved) == 0 {
		return RulesLock{}, fmt.Errorf("rules lock %s has unsupported or invalid format %q; review and run lintai lock", path, raw.Format)
	}
	wrapped := append([]byte(`{"format":"`+suppressions.Format+`","entries":`), raw.Approved...)
	approved, err := suppressions.Parse(path, append(wrapped, '}'))
	if err != nil {
		return RulesLock{}, fmt.Errorf("rules lock %s: %w", path, err)
	}
	if err := validatePolicies(approved, runInfo{}); err != nil {
		return RulesLock{}, fmt.Errorf("rules lock %s: %w", path, err)
	}
	return RulesLock{PackDigest: raw.PackDigest, PolicyDigest: raw.PolicyDigest, Suppressions: approved}, nil
}

// Reject ambiguous approvals, including duplicate members in nested allowance keys.
func uniqueObjectKeys(d *json.Decoder, envelope bool) error {
	token, err := d.Token()
	if err != nil {
		return err
	}
	start, container := token.(json.Delim)
	if !container {
		return nil
	}
	seen := map[string]bool{}
	for d.More() {
		if start == '{' {
			key, err := d.Token()
			if err != nil {
				return err
			}
			name := key.(string)
			if envelope {
				switch name {
				case "format", "abi", "pack_digest", "policy_digest", "approved_suppressions":
				default:
					return fmt.Errorf("unknown lock field %q", name)
				}
			}
			if seen[name] {
				return fmt.Errorf("duplicate field %q", name)
			}
			seen[name] = true
		}
		if err := uniqueObjectKeys(d, false); err != nil {
			return err
		}
	}
	_, err = d.Token()
	return err
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
	lock.observations, err = policyObservations(path, state)
	if err != nil {
		return RulesLock{}, err
	}
	return lock, nil
}

// Path settings are interpreted using the same workspace as producer preparation.
// Preserve application strings in all other producer and rule options.
func typeScriptPolicyConfig(root string, value any) (any, error) {
	raw, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("invalid TypeScript producer configuration")
	}
	config := maps.Clone(raw)
	portable := func(path string) (string, error) {
		if path == "" {
			return "", nil
		}
		if !filepath.IsAbs(path) {
			return filepath.ToSlash(filepath.Clean(path)), nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return "", err
		}
		return filepath.ToSlash(relative), nil
	}
	if links, ok := config["sourceLinks"].(string); ok {
		path, err := portable(links)
		if err != nil {
			return nil, err
		}
		config["sourceLinks"] = path
	}
	if sources, ok := config["sources"].([]string); ok {
		var paths []string
		for _, source := range sources {
			path, err := portable(source)
			if err != nil {
				return nil, err
			}
			paths = append(paths, path)
		}
		slices.Sort(paths)
		config["sources"] = slices.Compact(paths)
	}
	return config, nil
}

// policyDigest hashes effective Go policy before calling any executable entry.
func policyDigest(options Config, catalog producerCatalog) (string, error) {
	policies := map[string]any{}
	for id, policy := range options.Rule {
		if policy.Severity != "error" || len(policy.Options) != 0 {
			policies[id] = []any{policy.Severity, policy.Options}
		}
	}
	path := suppressions.ResolvePath(options.WorkspaceRoot, options.SuppressionsLocation)
	relative, err := filepath.Rel(options.WorkspaceRoot, path)
	if err != nil {
		return "", fmt.Errorf("suppression policy location: %w", err)
	}
	identity, err := catalogIdentity(catalog.entries, options.WorkspaceRoot)
	if err != nil {
		return "", err
	}
	raw, err := canonical.MarshalStructured([]any{"lintai-policy-v1", identity, policies, options.FactLimit, filepath.ToSlash(relative)})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("sha256:%x", sha256.Sum256(raw)), nil
}

func (lock RulesLock) verify(options Config, pack string, catalog producerCatalog) error {
	if pack != lock.PackDigest {
		return fmt.Errorf("rule pack digest changed (approved %s, found %s); review and run lintai lock", lock.PackDigest, pack)
	}
	policy, err := policyDigest(options, catalog)
	if err != nil {
		return err
	}
	if policy != lock.PolicyDigest {
		return fmt.Errorf("effective lint policy changed; review and run lintai lock")
	}
	return nil
}

// ApproveRules inspects the published pack and the effective policy used by
// Check and writes the rules lock to target in one guarded replacement. An
// empty target selects the default lock file, and a relative target is under
// the workspace root. It refuses a target that names the rule pack or the
// suppressions file, or an existing file that is not a rules lock, and writes
// nothing when the target or the suppressions file changes before the write.
func (e *Session) ApproveRules(ctx context.Context, target string) (RulesLock, error) {
	ctx, finish, err := e.beginCheck(ctx)
	if err != nil {
		return RulesLock{}, err
	}
	defer finish()
	options := e.config
	if target == "" {
		target = DefaultRulesLockFile
	}
	target = filepath.Clean(ResolveRulesLockPath(options.WorkspaceRoot, target))
	state, err := loadSuppressions(options, SuppressionUpdate{})
	if err != nil {
		return RulesLock{}, err
	}
	if sameDestination(target, state.path) || sameDestination(target, filepath.Join(options.WorkspaceRoot, rulepack.Path)) {
		return RulesLock{}, fmt.Errorf("lock path conflicts with rule pack or suppressions")
	}
	targetState, err := atomicfile.Read(target)
	if err != nil {
		return RulesLock{}, err
	}
	if targetState.Exists && !isRulesLockDocument(targetState.Data) {
		return RulesLock{}, fmt.Errorf("%s exists and is not a lintai rules lock; refusing to overwrite it", target)
	}
	digest, err := policyDigest(options, e.catalog)
	if err != nil {
		return RulesLock{}, err
	}
	catalog, err := openRuleCatalog(ctx, options.WorkspaceRoot, nil)
	if err != nil {
		return RulesLock{}, err
	}
	if err := catalog.loadMetadata(ctx, false); err != nil {
		return RulesLock{}, err
	}
	ids := map[string]struct{}{}
	for _, entry := range catalog.rules {
		if entry.loadError != nil {
			return RulesLock{}, fmt.Errorf("%s: %w", entry.path, entry.loadError)
		}
		if _, duplicate := ids[entry.metadata.ID]; duplicate {
			return RulesLock{}, fmt.Errorf("duplicate rule %s", entry.metadata.ID)
		}
		ids[entry.metadata.ID] = struct{}{}
	}
	if unknown := unknownPolicyRules(options, ids); len(unknown) > 0 {
		return RulesLock{}, fmt.Errorf("%s", unknown[0].Message)
	}
	lock := RulesLock{PackDigest: catalog.digest, PolicyDigest: digest, Suppressions: state.file}
	data, err := encodeRulesLock(target, lock)
	if err != nil {
		return RulesLock{}, err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return RulesLock{}, err
	}
	if e.hooks.beforeWrite != nil {
		e.hooks.beforeWrite()
	}
	if err := atomicfile.Write(ctx, target, targetState, data, "rules_lock_changed", state.path, state.snapshot, "suppression_changed"); err != nil {
		return RulesLock{}, err
	}
	return lock, nil
}

// sameDestination reports whether two paths name one file: through linked
// directories, including before the file exists, or through a link to an
// existing file.
func sameDestination(a, b string) bool {
	destination := func(path string) string {
		if dir, err := filepath.EvalSymlinks(filepath.Dir(path)); err == nil {
			return filepath.Join(dir, filepath.Base(path))
		}
		return filepath.Clean(path)
	}
	if destination(a) == destination(b) {
		return true
	}
	left, leftErr := os.Stat(a)
	right, rightErr := os.Stat(b)
	return leftErr == nil && rightErr == nil && os.SameFile(left, right)
}

// isRulesLockDocument accepts any branded lintai rules-lock format, including
// obsolete ones, so explicit reapproval can replace them.
func isRulesLockDocument(data []byte) bool {
	var header struct {
		Format string `json:"format"`
	}
	if json.Unmarshal(data, &header) != nil {
		return false
	}
	return strings.HasPrefix(header.Format, "lintai.rules-lock/")
}

func encodeRulesLock(path string, lock RulesLock) ([]byte, error) {
	approved := lock.Suppressions
	if approved == nil {
		approved = suppressions.File{}
	}
	canonicalApproved, err := suppressions.Encode(approved)
	if err != nil {
		return nil, err
	}
	approved, err = suppressions.Parse(path, canonicalApproved)
	if err != nil {
		return nil, err
	}
	allowances, err := json.Marshal(approved)
	if err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(lockJSON{RulesLockFormat, rulepack.ABI, lock.PackDigest, lock.PolicyDigest, allowances}, "", "  ")
	if err != nil {
		return nil, err
	}
	data = append(data, '\n')
	if _, err := parseRulesLock(path, data); err != nil {
		return nil, err
	}
	return data, nil
}

func ResolveRulesLockPath(root, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(root, path)
}
