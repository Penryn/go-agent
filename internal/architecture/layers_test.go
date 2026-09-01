package architecture_test

import (
	"bufio"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const moduleInternal = "github.com/phlin/go-agent/internal/"

// TestLayerDependencies keeps the directory layout meaningful. It checks
// production imports only; tests may use adapters as fixtures.
func TestLayerDependencies(t *testing.T) {
	root := repositoryRoot(t)
	internalRoot := filepath.Join(root, "internal")
	fset := token.NewFileSet()

	err := filepath.WalkDir(internalRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(internalRoot, path)
		if err != nil {
			return err
		}
		layer := strings.Split(filepath.ToSlash(rel), "/")[0]

		for _, spec := range file.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return err
			}
			if violation := forbiddenImport(layer, importPath); violation != "" {
				position := fset.Position(spec.Pos())
				t.Errorf("%s:%d: %s", filepath.ToSlash(rel), position.Line, violation)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func forbiddenImport(layer, importPath string) string {
	if !strings.HasPrefix(importPath, moduleInternal) {
		return ""
	}
	dependency := strings.TrimPrefix(importPath, moduleInternal)

	switch layer {
	case "domain":
		if !strings.HasPrefix(dependency, "domain/") {
			return fmt.Sprintf("domain must not depend on %q", dependency)
		}
	case "search":
		return fmt.Sprintf("search must stay independent of internal package %q", dependency)
	case "application":
		if strings.HasPrefix(dependency, "adapters/") || strings.HasPrefix(dependency, "app/") {
			return fmt.Sprintf("application must not depend on %q", dependency)
		}
	case "adapters":
		if strings.HasPrefix(dependency, "app/") {
			return fmt.Sprintf("adapter must not depend on composition root %q", dependency)
		}
	}
	return ""
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		file, err := os.Open(filepath.Join(dir, "go.mod"))
		if err == nil {
			scanner := bufio.NewScanner(file)
			for scanner.Scan() {
				if strings.TrimSpace(scanner.Text()) == "module github.com/phlin/go-agent" {
					_ = file.Close()
					return dir
				}
			}
			_ = file.Close()
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("repository root not found")
		}
		dir = parent
	}
}
