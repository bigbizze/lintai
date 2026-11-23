package bundle

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// SetupSandbox selects whether setup() runs under the Node permission model.
type SetupSandbox string

const (
	// SetupSandboxOn sandboxes setup().
	SetupSandboxOn SetupSandbox = "on"
	// SetupSandboxOff runs setup() with full Node capabilities.
	SetupSandboxOff SetupSandbox = "off"
)

// ParseSetupSandbox parses on or off. An empty value defaults to on.
func ParseSetupSandbox(value string) (SetupSandbox, error) {
	switch SetupSandbox(value) {
	case "", SetupSandboxOn:
		return SetupSandboxOn, nil
	case SetupSandboxOff:
		return SetupSandboxOff, nil
	}
	return "", fmt.Errorf("invalid setup sandbox %q: use on or off", value)
}

var stableNodeVersion = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

// NodeExecutable resolves and validates the Node executable that will actually
// be launched. Resolve it each time so PATH changes cannot bypass the check.
func NodeExecutable(ctx context.Context) (string, error) {
	const requirement = "install stable Node >=26.13.1 and <27 and put it on PATH"
	node, err := exec.LookPath("node")
	if err != nil {
		return "", fmt.Errorf("could not find node: %w; %s", err, requirement)
	}
	node, err = filepath.Abs(node)
	if err != nil {
		return "", fmt.Errorf("could not resolve node executable: %w; %s", err, requirement)
	}
	output, err := exec.CommandContext(ctx, node, "--version").Output()
	if err != nil {
		return "", fmt.Errorf("could not run %s --version: %w; %s", node, err, requirement)
	}
	version := strings.TrimSpace(string(output))
	parts := stableNodeVersion.FindStringSubmatch(version)
	if parts == nil {
		return "", fmt.Errorf("unsupported Node version %q from %s; %s (prereleases are not supported)", version, node, requirement)
	}
	major, majorErr := strconv.Atoi(parts[1])
	minor, minorErr := strconv.Atoi(parts[2])
	patch, patchErr := strconv.Atoi(parts[3])
	if majorErr != nil || minorErr != nil || patchErr != nil || major != 26 || minor < 13 || (minor == 13 && patch < 1) {
		return "", fmt.Errorf("unsupported Node version %q from %s; %s", version, node, requirement)
	}
	return node, nil
}

// sandboxArgs returns the node arguments that run the prepare script under
// the permission model. The process can read the workspace, the bundles and
// the asset root. It cannot write files, start child processes or workers,
// or access the network.
//
// Node resolves the real path of the script and of each module, so a path
// that goes through a symbolic link is also allowed as its real path.
func sandboxArgs(scriptPath, assetRoot, workspaceRoot string, artifacts map[string]Artifacts) []string {
	readable := make(map[string]struct{})
	allow := func(path string) {
		readable[filepath.Clean(path)] = struct{}{}
		if real, err := filepath.EvalSymlinks(path); err == nil {
			readable[real] = struct{}{}
		}
	}
	allow(workspaceRoot)
	allow(assetRoot)
	for _, item := range artifacts {
		allow(filepath.Dir(item.PrepareBundlePath))
	}
	paths := make([]string, 0, len(readable))
	for path := range readable {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	args := []string{"--permission"}
	for _, path := range paths {
		args = append(args, "--allow-fs-read="+path)
	}
	if real, err := filepath.EvalSymlinks(scriptPath); err == nil {
		scriptPath = real
	}
	return append(args, scriptPath)
}
