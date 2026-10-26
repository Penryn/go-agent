package architecture

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestRuntimeHasNoLegacyOrExecutionDependencies(t *testing.T) {
	err := filepath.WalkDir("..", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, im := range f.Imports {
			value, _ := strconv.Unquote(im.Path.Value)
			if value == "os/exec" || strings.Contains(value, "modelcontextprotocol") || strings.Contains(value, "/application/") || strings.Contains(value, "/domain/") || strings.Contains(value, "/adapters/") || strings.Contains(value, "/eino/adk") || strings.Contains(value, "/eino/compose") {
				t.Errorf("%s imports removed execution/legacy dependency %s", path, value)
			}
			if strings.Contains(path, string(filepath.Separator)+"search"+string(filepath.Separator)) && (value == "database/sql" || value == "net/http") {
				t.Errorf("search algorithm performs I/O: %s", path)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
