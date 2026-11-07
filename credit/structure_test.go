package credit

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The guards of the branch's lifecycle and of the order exposure, as checks (internal/AGENTS.md, "规则只在一处"): a
// branch's state is the SDK's (be-sdk-go reserve, be-protocol chapter 16), so nothing here writes the runtime's
// tables; the order exposure of a limit is written only by internal/limits/exposure.go.

var (
	writesReserve  = regexp.MustCompile(`(?is)\b(INSERT\s+INTO|UPDATE|DELETE\s+FROM)\s+be_reserve_(branch|consumption)\b`)
	writesExposure = regexp.MustCompile(`(?is)\border_exposure\s*=`)
)

const exposureOwner = "internal/limits/exposure.go"

// sqlLiterals calls f with every string literal of the non-test Go files under internal/, by file relative to the
// repository.
func sqlLiterals(t *testing.T, f func(file string, line int, s string)) {
	root, err := filepath.Abs("..")
	require.NoError(t, err)
	n := 0
	require.NoError(t, filepath.WalkDir(filepath.Join(root, "internal"), func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		n++
		ast.Inspect(file, func(node ast.Node) bool {
			if lit, ok := node.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				if s, err := strconv.Unquote(lit.Value); err == nil {
					f(rel, fset.Position(lit.Pos()).Line, s)
				}
			}
			return true
		})
		return nil
	}))
	require.NotZero(t, n, "the check reads the code under internal/ (else it checks nothing)")
}

// TestTheBranchStateIsTheSDKs: no SQL here writes the participant runtime's tables; reserve's Hold, Confirm, Cancel,
// Consume and ExpireDue do.
func TestTheBranchStateIsTheSDKs(t *testing.T) {
	sqlLiterals(t, func(file string, line int, s string) {
		if writesReserve.MatchString(s) {
			t.Errorf("%s:%d writes the SDK's branch tables: use reserve's commands", file, line)
		}
	})
}

// TestTheOrderExposureHasOneOwner: only internal/limits/exposure.go writes a limit's order exposure (TakeOrder,
// ConsumeOrder, GiveBackOrder), each raising the limit's version and publishing it.
func TestTheOrderExposureHasOneOwner(t *testing.T) {
	found := 0
	sqlLiterals(t, func(file string, line int, s string) {
		if !writesExposure.MatchString(s) {
			return
		}
		if file == exposureOwner {
			found++
			return
		}
		t.Errorf("%s:%d writes the order exposure: use the functions of %s", file, line, exposureOwner)
	})
	assert.NotZero(t, found, "the check finds the writes in "+exposureOwner)
}
