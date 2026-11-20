package bundle

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

const InputManifestFormat = "lintai.bundle-inputs/1"

type Artifacts struct {
	PureBundlePath    string `json:"pureBundlePath"`
	PrepareBundlePath string `json:"prepareBundlePath"`
	// Hash covers the bundle inputs and the bundler and entry-generation identity.
	Hash     string        `json:"hash"`
	Manifest InputManifest `json:"manifest"`
}

// InputManifest is owned by the bundler and validated before an artifact is
// published or reused. Absolute paths are used only to check freshness.
type InputManifest struct {
	Format      string        `json:"format"`
	Inputs      []Input       `json:"inputs"`
	Probes      []ConfigProbe `json:"probes"`
	Resolutions []string      `json:"resolutions"`
	Tool        ToolIdentity  `json:"tool"`
}

type ToolIdentity struct {
	Esbuild        string `json:"esbuild"`
	ConfigResolver string `json:"configResolver"`
	Script         string `json:"script"`
	BinaryOverride string `json:"binaryOverride"`
}

// Input is a file consumed by a rule bundle or its config resolver. Path is
// workspace-relative, repo: or asset: relative, or external: workspace-relative.
type Input struct {
	Path         string `json:"path"`
	SHA256       string `json:"sha256"`
	AbsolutePath string `json:"absolutePath"`
}

// ConfigProbe is a resolver observation used to validate bundle reuse.
type ConfigProbe struct {
	Path         string   `json:"path"`
	LogicalPath  string   `json:"logicalPath"`
	Directory    bool     `json:"directory"`
	Exists       bool     `json:"exists"`
	Digest       string   `json:"digest,omitempty"`
	Ignore       []string `json:"ignore,omitempty"`
	LinkTarget   string   `json:"linkTarget,omitempty"`
	LinkIdentity string   `json:"linkIdentity,omitempty"`
}

func (p *ConfigProbe) UnmarshalJSON(data []byte) error {
	var value struct {
		Path         string   `json:"path"`
		LogicalPath  string   `json:"logicalPath"`
		Directory    *bool    `json:"directory"`
		Exists       *bool    `json:"exists"`
		Digest       string   `json:"digest"`
		Ignore       []string `json:"ignore"`
		LinkTarget   string   `json:"linkTarget"`
		LinkIdentity string   `json:"linkIdentity"`
	}
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	if value.Directory == nil || value.Exists == nil {
		return fmt.Errorf("bundle probe is missing its type or existence observation")
	}
	*p = ConfigProbe{Path: value.Path, LogicalPath: value.LogicalPath, Directory: *value.Directory, Exists: *value.Exists, Digest: value.Digest, Ignore: value.Ignore, LinkTarget: value.LinkTarget, LinkIdentity: value.LinkIdentity}
	return nil
}

func (m InputManifest) validate() error {
	if m.Format != InputManifestFormat || len(m.Inputs) == 0 || m.Probes == nil || m.Resolutions == nil || m.Tool.Esbuild == "" || m.Tool.ConfigResolver == "" || !validDigest(m.Tool.Script) {
		return fmt.Errorf("invalid or unsupported bundle input manifest %q", m.Format)
	}
	paths := make(map[string]string)
	checkPath := func(logical, absolute string) error {
		if previous, ok := paths[logical]; ok && previous != absolute {
			return fmt.Errorf("bundle input paths collide at %q", logical)
		}
		paths[logical] = absolute
		return nil
	}
	for _, input := range m.Inputs {
		if input.Path == "" || !filepath.IsAbs(input.AbsolutePath) || !validDigest(input.SHA256) {
			return fmt.Errorf("invalid bundle input %q", input.Path)
		}
		if err := checkPath(input.Path, input.AbsolutePath); err != nil {
			return err
		}
	}
	for _, probe := range m.Probes {
		if probe.LogicalPath == "" || !filepath.IsAbs(probe.Path) || (probe.Digest != "" && (!probe.Directory || !probe.Exists || !validDigest(probe.Digest))) || (probe.LinkTarget != "" && probe.LinkIdentity == "") {
			return fmt.Errorf("invalid bundle input probe %q", probe.Path)
		}
		if err := checkPath(probe.LogicalPath, probe.Path); err != nil {
			return err
		}
	}
	return nil
}

func validDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

// Unchanged checks exactly the input files and observations supplied by the
// bundler. It does not discover additional inputs.
func (m InputManifest) Unchanged() bool {
	return m.currentError() == nil
}

func (m InputManifest) currentError() error {
	if err := m.validate(); err != nil {
		return err
	}
	if os.Getenv("ESBUILD_BINARY_PATH") != m.Tool.BinaryOverride {
		return fmt.Errorf("esbuild binary selection changed")
	}
	for _, input := range m.Inputs {
		contents, err := os.ReadFile(input.AbsolutePath)
		if err != nil {
			return fmt.Errorf("read %s: %w", input.AbsolutePath, err)
		}
		digest := sha256.Sum256(contents)
		if hex.EncodeToString(digest[:]) != input.SHA256 {
			return fmt.Errorf("content changed: %s", input.AbsolutePath)
		}
	}
	for _, probe := range m.Probes {
		if probe.LinkTarget != "" {
			target, err := os.Readlink(probe.Path)
			if err != nil || target != probe.LinkTarget {
				return fmt.Errorf("link changed: %s", probe.Path)
			}
		}
		info, err := os.Stat(probe.Path)
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
			if probe.Exists {
				return fmt.Errorf("input disappeared: %s", probe.Path)
			}
			continue
		}
		if err != nil {
			return fmt.Errorf("stat %s: %w", probe.Path, err)
		}
		exists := info.Mode().IsRegular()
		if probe.Directory {
			exists = info.IsDir()
		}
		if exists != probe.Exists {
			return fmt.Errorf("input presence changed: %s", probe.Path)
		}
		if probe.Digest != "" {
			digest, err := directoryDigest(probe.Path, probe.Ignore)
			if err != nil || digest != probe.Digest {
				return fmt.Errorf("directory changed: %s", probe.Path)
			}
		}
	}
	return nil
}

func directoryDigest(directory string, ignore []string) (string, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	for _, entry := range entries {
		if entry.Name() == "lintai-rules.lock.json" || contains(ignore, entry.Name()) {
			continue
		}
		kind := "f"
		if entry.IsDir() {
			kind = "d"
		}
		fmt.Fprintf(hash, "%s:%s\x00", kind, entry.Name())
		if !entry.IsDir() && (entry.Name() == "package.json" || entry.Name() == "jsconfig.json" || strings.HasPrefix(entry.Name(), "tsconfig") && strings.HasSuffix(entry.Name(), ".json")) {
			contents, err := os.ReadFile(filepath.Join(directory, entry.Name()))
			if err != nil {
				return "", err
			}
			digest := sha256.Sum256(contents)
			hash.Write(digest[:])
			hash.Write([]byte{0})
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func contains(items []string, value string) bool {
	for _, item := range items {
		if item == value {
			return true
		}
	}
	return false
}

type PrepareResult struct {
	RuleID      string `json:"ruleId"`
	RuleVersion int    `json:"ruleVersion"`
	Env         any    `json:"env"`
	Setup       any    `json:"setup"`
}

type PrepareError struct {
	RuleID      string
	RuleVersion int
	Message     string
}

type buildRequest struct {
	RepoRoot      string `json:"repoRoot"`
	WorkspaceRoot string `json:"workspaceRoot"`
	RulePath      string `json:"rulePath"`
	OutDir        string `json:"outDir"`
	OutputRoot    string `json:"outputRoot"`
}

type buildResponse struct {
	RulePath          string          `json:"rulePath"`
	PrepareBundlePath string          `json:"prepareBundlePath,omitempty"`
	PureBundlePath    string          `json:"pureBundlePath,omitempty"`
	Hash              string          `json:"hash,omitempty"`
	Manifest          json.RawMessage `json:"manifest"`
	Error             string          `json:"error,omitempty"`
}

type prepareRequest struct {
	RulePath      string `json:"rulePath"`
	WorkspaceRoot string `json:"workspaceRoot"`
	BundlePath    string `json:"bundlePath"`
	Env           any    `json:"env"`
	RunSetup      bool   `json:"runSetup"`
	// RestrictRequire limits require() in the prepare bundle to Node
	// built-ins and makes dynamic import() fail.
	RestrictRequire bool `json:"restrictRequire"`
}

type prepareResponse struct {
	RulePath    string `json:"rulePath"`
	RuleID      string `json:"ruleId,omitempty"`
	RuleVersion int    `json:"ruleVersion,omitempty"`
	Env         any    `json:"env,omitempty"`
	Setup       any    `json:"setup,omitempty"`
	Error       string `json:"error,omitempty"`
	Skipped     bool   `json:"skipped,omitempty"`
}

// BuildAll writes the bundles of each rule to its own subdirectory of outDir,
// and returns the rules-lock hash of each rule. The caller owns outDir and
// removes it when the bundles are no longer needed.
func BuildAll(ctx context.Context, assetRoot, repoRoot, workspaceRoot, outDir string, rulePaths []string) (map[string]Artifacts, map[string]error, error) {
	if len(rulePaths) == 0 {
		return map[string]Artifacts{}, map[string]error{}, nil
	}

	requests := make([]buildRequest, 0, len(rulePaths))
	requested := make(map[string]struct{}, len(rulePaths))
	for index, rulePath := range rulePaths {
		if _, exists := requested[rulePath]; exists {
			return nil, nil, fmt.Errorf("duplicate rule path %q", rulePath)
		}
		requested[rulePath] = struct{}{}
		requests = append(requests, buildRequest{
			RepoRoot:      repoRoot,
			WorkspaceRoot: workspaceRoot,
			RulePath:      rulePath,
			OutDir:        filepath.Join(outDir, strconv.Itoa(index)),
			OutputRoot:    outDir,
		})
	}
	itemErrors := make(map[string]error)

	payload, err := json.Marshal(requests)
	if err != nil {
		return nil, nil, err
	}
	scriptPath, err := resolveScriptPath(assetRoot, "bundle-rule.mjs")
	if err != nil {
		return nil, itemErrors, err
	}
	stagedRoot, stagedScript, err := stageBundleHelper(assetRoot, scriptPath)
	if err != nil {
		return nil, itemErrors, err
	}
	defer os.RemoveAll(stagedRoot)
	absoluteAssetRoot, err := filepath.Abs(assetRoot)
	if err != nil {
		return nil, itemErrors, err
	}
	command := exec.CommandContext(ctx, "node", stagedScript, absoluteAssetRoot, stagedRoot)
	command.Dir = assetRoot
	command.Stdin = bytes.NewReader(payload)
	output, err := command.CombinedOutput()
	if err != nil {
		return nil, itemErrors, fmt.Errorf("bundle failed: %w\n%s", err, output)
	}

	var rows []buildResponse
	if err := json.Unmarshal(output, &rows); err != nil {
		return nil, itemErrors, fmt.Errorf("bundle output was not valid JSON: %w", err)
	}

	results := make(map[string]Artifacts, len(rows))
	seen := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		if _, expected := requested[row.RulePath]; !expected {
			return nil, itemErrors, fmt.Errorf("bundle returned unexpected rule path %q", row.RulePath)
		}
		if _, exists := seen[row.RulePath]; exists {
			return nil, itemErrors, fmt.Errorf("bundle returned duplicate rule path %q", row.RulePath)
		}
		seen[row.RulePath] = struct{}{}
		if row.Error != "" {
			itemErrors[row.RulePath] = fmt.Errorf("%s", row.Error)
			continue
		}
		if !strings.HasPrefix(row.Hash, "sha256:") || !validDigest(strings.TrimPrefix(row.Hash, "sha256:")) {
			itemErrors[row.RulePath] = fmt.Errorf("invalid bundle artifact hash")
			continue
		}
		var manifest InputManifest
		if err := json.Unmarshal(row.Manifest, &manifest); err != nil {
			itemErrors[row.RulePath] = fmt.Errorf("invalid bundle input manifest: %w", err)
			continue
		}
		if err := manifest.currentError(); err != nil {
			itemErrors[row.RulePath] = fmt.Errorf("bundle inputs changed during compilation: %w", err)
			continue
		}
		results[row.RulePath] = Artifacts{
			PureBundlePath:    row.PureBundlePath,
			PrepareBundlePath: row.PrepareBundlePath,
			Hash:              row.Hash,
			Manifest:          manifest,
		}
	}

	for _, request := range requests {
		if _, exists := seen[request.RulePath]; !exists {
			return nil, itemErrors, fmt.Errorf("bundle returned no result for %q", request.RulePath)
		}
	}
	return results, itemErrors, nil
}

// stageBundleHelper gives Node the same helper bytes that the manifest later
// validates against the asset root. The repository-root launcher imports the
// client helper, so both files are copied when that layout is present.
func stageBundleHelper(assetRoot, scriptPath string) (root, stagedScript string, err error) {
	assetRoot, err = filepath.Abs(assetRoot)
	if err != nil {
		return "", "", err
	}
	scriptPath, err = filepath.Abs(scriptPath)
	if err != nil {
		return "", "", err
	}
	createdRoot, err := os.MkdirTemp("", "lintai-bundle-helper-")
	if err != nil {
		return "", "", err
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(createdRoot)
		}
	}()
	root, err = filepath.EvalSymlinks(createdRoot)
	if err != nil {
		return "", "", err
	}
	copyScript := func(relative string) error {
		contents, err := os.ReadFile(filepath.Join(assetRoot, relative))
		if err != nil {
			return err
		}
		destination := filepath.Join(root, relative)
		if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
			return err
		}
		return os.WriteFile(destination, contents, 0o600)
	}
	if err = copyScript(filepath.Join("scripts", "bundle-rule.mjs")); err != nil {
		return "", "", err
	}
	helperDir := filepath.Dir(scriptPath)
	clientHelper := filepath.Join("packages", "client", "scripts", "bundle-rule.mjs")
	if _, statErr := os.Stat(filepath.Join(assetRoot, clientHelper)); statErr == nil {
		if err = copyScript(clientHelper); err != nil {
			return "", "", err
		}
		helperDir = filepath.Dir(filepath.Join(assetRoot, clientHelper))
	} else if !os.IsNotExist(statErr) {
		return "", "", statErr
	}
	for _, name := range []string{"esbuild", "typescript", filepath.Join("@lintai", "sdk")} {
		var found string
		for directory := helperDir; ; directory = filepath.Dir(directory) {
			candidate := filepath.Join(directory, "node_modules", name)
			if _, statErr := os.Stat(candidate); statErr == nil {
				found, err = filepath.Abs(candidate)
				break
			} else if !os.IsNotExist(statErr) {
				return "", "", statErr
			}
			if directory == filepath.Dir(directory) {
				break
			}
		}
		if err != nil {
			return "", "", err
		}
		if found == "" {
			return "", "", fmt.Errorf("bundle helper dependency %q was not found", name)
		}
		link := filepath.Join(root, "node_modules", name)
		if err = os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
			return "", "", err
		}
		if err = os.Symlink(found, link); err != nil {
			return "", "", err
		}
	}
	return root, filepath.Join(root, "scripts", "bundle-rule.mjs"), nil
}

// PrepareOptions control the process that runs setup().
type PrepareOptions struct {
	// Sandbox runs the process under the Node permission model: it can read
	// only the workspace, the bundles and the asset root, and it cannot
	// write files or start child processes or workers.
	Sandbox bool
	// RestrictRequire limits require() in setup() to Node built-ins and
	// makes dynamic import() fail. It is always on when Sandbox is on.
	RestrictRequire bool
	// SkipSetup identifies rules that need config parsing but do not declare
	// setup(). Their setup bundle still validates defaults without invoking it.
	SkipSetup map[string]struct{}
}

// PrepareAll runs setup() for each rule. A file under the rule globs that has
// no default export is a helper module, not a rule: it is in neither result
// map.
func PrepareAll(ctx context.Context, assetRoot, repoRoot, workspaceRoot string, artifacts map[string]Artifacts, rawEnv any, options PrepareOptions) (map[string]PrepareResult, map[string]PrepareError, error) {
	if len(artifacts) == 0 {
		return map[string]PrepareResult{}, map[string]PrepareError{}, nil
	}

	requests := make([]prepareRequest, 0, len(artifacts))
	requested := make(map[string]struct{}, len(artifacts))
	rulePaths := make([]string, 0, len(artifacts))
	for rulePath := range artifacts {
		rulePaths = append(rulePaths, rulePath)
	}
	sort.Strings(rulePaths)
	for _, rulePath := range rulePaths {
		item := artifacts[rulePath]
		if _, exists := requested[rulePath]; exists {
			return nil, nil, fmt.Errorf("duplicate rule path %q", rulePath)
		}
		requested[rulePath] = struct{}{}
		requests = append(requests, prepareRequest{
			RulePath:        rulePath,
			WorkspaceRoot:   workspaceRoot,
			BundlePath:      item.PrepareBundlePath,
			Env:             rawEnv,
			RunSetup:        true,
			RestrictRequire: options.Sandbox || options.RestrictRequire,
		})
		if _, skip := options.SkipSetup[rulePath]; skip {
			requests[len(requests)-1].RunSetup = false
		}
	}

	payload, err := json.Marshal(requests)
	if err != nil {
		return nil, nil, err
	}
	scriptPath, err := resolveScriptPath(assetRoot, "prepare-rule.mjs")
	if err != nil {
		return nil, nil, err
	}
	args := []string{scriptPath}
	if options.Sandbox {
		args = sandboxArgs(scriptPath, assetRoot, workspaceRoot, artifacts)
	}
	command := exec.CommandContext(ctx, "node", args...)
	command.Dir = assetRoot
	command.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		return nil, nil, fmt.Errorf("prepare failed: %w\n%s%s", err, output, stderr.Bytes())
	}

	var rows []prepareResponse
	if err := json.Unmarshal(output, &rows); err != nil {
		return nil, nil, fmt.Errorf("prepare output was not valid JSON: %w", err)
	}

	results := make(map[string]PrepareResult, len(rows))
	itemErrors := make(map[string]PrepareError)
	seen := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		if _, expected := requested[row.RulePath]; !expected {
			return nil, nil, fmt.Errorf("prepare returned unexpected rule path %q", row.RulePath)
		}
		if _, exists := seen[row.RulePath]; exists {
			return nil, nil, fmt.Errorf("prepare returned duplicate rule path %q", row.RulePath)
		}
		seen[row.RulePath] = struct{}{}
		if row.Skipped {
			continue
		}
		if row.Error != "" {
			itemErrors[row.RulePath] = PrepareError{
				RuleID:      row.RuleID,
				RuleVersion: row.RuleVersion,
				Message:     row.Error,
			}
			continue
		}
		results[row.RulePath] = PrepareResult{
			RuleID:      row.RuleID,
			RuleVersion: row.RuleVersion,
			Env:         row.Env,
			Setup:       row.Setup,
		}
	}

	for rulePath := range artifacts {
		if _, exists := seen[rulePath]; !exists {
			return nil, nil, fmt.Errorf("prepare returned no result for %q", rulePath)
		}
	}
	return results, itemErrors, nil
}

func resolveScriptPath(assetRoot, name string) (string, error) {
	if assetRoot == "" {
		return "", fmt.Errorf("lintai asset root is required")
	}
	scriptPath := filepath.Join(assetRoot, "scripts", name)
	if _, err := os.Stat(scriptPath); err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("lintai helper script %q was not found under asset root %q", name, assetRoot)
		}
		return "", err
	}
	return scriptPath, nil
}
