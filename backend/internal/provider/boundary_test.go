package provider_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// Prevent SDK imports from creeping into domain/use-case/transport code as the
// Gmail adapter is implemented. Composition may import the adapter, not its SDK.
func TestGmailSDKImportsStayInsideAdapter(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate test source")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if strings.HasPrefix(filepath.ToSlash(relative), "internal/provider/gmail/") {
			return nil
		}
		source, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, spec := range source.Imports {
			imported, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return err
			}
			if imported == "google.golang.org/api/gmail" || strings.HasPrefix(imported, "google.golang.org/api/gmail/") {
				t.Errorf("Gmail SDK import outside adapter: %s", relative)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
