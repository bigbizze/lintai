package typescript

import "fmt"

type FileScope string

const (
	FileScopeTsconfig FileScope = "tsconfig"
	FileScopeWalk     FileScope = "walk"
)

// Options are TypeScript producer settings and are fixed when the producer
// instance is constructed.
type Options struct {
	Excludes    []string
	Files       FileScope
	SourceLinks string
}

func ParseFileScope(value string) (FileScope, error) {
	switch FileScope(value) {
	case "", FileScopeTsconfig:
		return FileScopeTsconfig, nil
	case FileScopeWalk:
		return FileScopeWalk, nil
	default:
		return "", fmt.Errorf("invalid file scope %q: use tsconfig or walk", value)
	}
}
