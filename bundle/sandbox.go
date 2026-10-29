package bundle

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// SetupSandbox selects whether setup() runs under the Node permission model.
type SetupSandbox string

const (
	// SetupSandboxAuto sandboxes setup() when Node supports the permission
	// model, and otherwise runs it as before with a warning.
	SetupSandboxAuto SetupSandbox = "auto"
	// SetupSandboxOn sandboxes setup() and fails when Node does not support
	// the permission model.
	SetupSandboxOn SetupSandbox = "on"
	// SetupSandboxOff runs setup() with full Node capabilities.
	SetupSandboxOff SetupSandbox = "off"
)

// ParseSetupSandbox parses auto, on or off. An empty value is auto.
func ParseSetupSandbox(value string) (SetupSandbox, error) {
	switch SetupSandbox(value) {
	case "", SetupSandboxAuto:
		return SetupSandboxAuto, nil
	case SetupSandboxOn, SetupSandboxOff:
		return SetupSandbox(value), nil
	}
	return "", fmt.Errorf("invalid setup sandbox %q: use auto, on or off", value)
}

var nodeVersion = sync.OnceValues(func() ([3]int, error) {
	output, err := exec.CommandContext(context.Background(), "node", "--version").Output()
	if err != nil {
		return [3]int{}, fmt.Errorf("could not run node --version: %w", err)
	}
	return parseNodeVersion(strings.TrimSpace(string(output)))
})

func parseNodeVersion(text string) ([3]int, error) {
	parts := strings.SplitN(strings.TrimPrefix(text, "v"), ".", 3)
	if len(parts) != 3 {
		return [3]int{}, fmt.Errorf("unexpected node --version output %q", text)
	}
	var version [3]int
	for index, part := range parts {
		number, err := strconv.Atoi(strings.SplitN(part, "-", 2)[0])
		if err != nil {
			return [3]int{}, fmt.Errorf("unexpected node --version output %q", text)
		}
		version[index] = number
	}
	return version, nil
}

// supportsSandbox reports a Node release with the stable --permission flag:
// 22.13 and later 22.x releases, and 23.5 and later. Node 23.0 to 23.4 has
// only --experimental-permission.
func supportsSandbox(version [3]int) bool {
	major, minor := version[0], version[1]
	switch {
	case major == 22:
		return minor >= 13
	case major == 23:
		return minor >= 5
	}
	return major > 23
}

// ResolveSetupSandbox decides whether setup() runs in the sandbox. It checks
// the Node version once per process. For auto on an old Node, it returns
// false and a warning to print. For on, an old Node is an error.
func ResolveSetupSandbox(mode SetupSandbox) (sandbox bool, warning string, err error) {
	if mode == SetupSandboxOff {
		return false, "", nil
	}
	version, err := nodeVersion()
	if err != nil {
		if mode == SetupSandboxOn {
			return false, "", err
		}
		return false, fmt.Sprintf("lintai: setup() runs without a sandbox: %v", err), nil
	}
	if supportsSandbox(version) {
		return true, "", nil
	}
	text := fmt.Sprintf("Node %d.%d.%d does not support the permission model; the setup sandbox needs Node 22.13 or later, or 23.5 or later", version[0], version[1], version[2])
	if mode == SetupSandboxOn {
		return false, "", fmt.Errorf("--setup-sandbox=on: %s", text)
	}
	return false, "lintai: setup() runs without a sandbox: " + text, nil
}

// sandboxArgs returns the node arguments that run the prepare script under
// the permission model. The process can read the workspace, the bundles and
// the asset root. It cannot write files, start child processes or workers.
// On Node 25 and later the network is also denied, because no --allow-net
// is given. On Node 22.13 to 24 the permission model does not restrict the
// network.
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
