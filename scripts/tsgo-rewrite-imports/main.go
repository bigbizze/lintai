// Command tsgo-rewrite-imports rewrites import paths in Go files for
// scripts/sync-tsgo.sh. It edits only import specs and can add a header line.
//
// Usage: tsgo-rewrite-imports -from PREFIX -to PREFIX [-base DIR] [-header TEXT] FILE...
//
// In the header text, {path} is replaced by the file path given on the
// command line, without the -base prefix.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"slices"
	"strconv"
	"strings"
)

func main() {
	from := flag.String("from", "", "import path prefix to replace")
	to := flag.String("to", "", "replacement import path prefix")
	header := flag.String("header", "", "line comment to add as the first line, without the leading //")
	base := flag.String("base", "", "prefix to remove from file paths in the header")
	flag.Parse()
	if *from == "" || *to == "" {
		fmt.Fprintln(os.Stderr, "tsgo-rewrite-imports: -from and -to are required")
		os.Exit(2)
	}
	for _, path := range flag.Args() {
		if err := rewrite(path, *from, *to, strings.ReplaceAll(*header, "{path}", strings.TrimPrefix(path, *base))); err != nil {
			fmt.Fprintf(os.Stderr, "tsgo-rewrite-imports: %s: %v\n", path, err)
			os.Exit(1)
		}
	}
}

type edit struct {
	start, end int
	text       string
}

func rewrite(path, from, to, header string) error {
	src, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, src, parser.ImportsOnly|parser.ParseComments)
	if err != nil {
		return err
	}
	var edits []edit
	for _, spec := range file.Imports {
		value, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			return err
		}
		if value != strings.TrimSuffix(from, "/") && !strings.HasPrefix(value, from) {
			continue
		}
		start := fset.Position(spec.Path.Pos()).Offset
		end := fset.Position(spec.Path.End()).Offset
		edits = append(edits, edit{start, end, strconv.Quote(to + strings.TrimPrefix(value, from))})
	}
	if len(edits) == 0 && header == "" {
		return nil
	}
	slices.SortFunc(edits, func(a, b edit) int { return b.start - a.start })
	out := slices.Clone(src)
	for _, item := range edits {
		out = slices.Concat(out[:item.start], []byte(item.text), out[item.end:])
	}
	if header != "" {
		out = slices.Concat([]byte("// "+header+"\n\n"), out)
	}
	formatted, err := format.Source(out)
	if err != nil {
		return err
	}
	if bytes.Equal(formatted, src) {
		return nil
	}
	return os.WriteFile(path, formatted, 0o644)
}
