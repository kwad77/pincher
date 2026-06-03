// SPDX-License-Identifier: MIT

package db

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"sort"
	"strings"
	"testing"
)

// TestReaderRoutedMethods_NeverTouchWriterConnection_1826 is the
// structural gate for #1826.
//
// readerRoutedStoreMethods (db_test.go) classifies a method as a pure
// read. TestStore_AllExportedMethodsClassified only checks the method
// is *listed* — not that the implementation matches. #1824 shipped
// because of exactly that gap: TraceViaCTEScoped was correctly
// classified reader-routed, but its unexported helper traceViaCTE ran
// its SELECT on s.db — the single-writer connection
// (SetMaxOpenConns(1)) — so every trace serialized behind indexer
// writes. The HealthCheck comment in db_test.go documents the first
// time the same bug shipped.
//
// This gate AST-walks each reader-classified method AND every *Store
// method it transitively calls, and fails if any of them reaches the
// receiver's `.db` field. A reader-routed method must touch only
// `.ro`, the reader pool.
func TestReaderRoutedMethods_NeverTouchWriterConnection_1826(t *testing.T) {
	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("readdir package db: %v", err)
	}

	// storeMethods: method name -> its FuncDecl, for every method
	// declared on *Store across the package's non-test files.
	storeMethods := map[string]*ast.FuncDecl{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, perr := parser.ParseFile(fset, name, nil, 0)
		if perr != nil {
			t.Fatalf("parse %s: %v", name, perr)
		}
		for _, decl := range f.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Body == nil || fd.Recv == nil || len(fd.Recv.List) != 1 {
				continue
			}
			star, ok := fd.Recv.List[0].Type.(*ast.StarExpr)
			if !ok {
				continue
			}
			if id, ok := star.X.(*ast.Ident); ok && id.Name == "Store" {
				storeMethods[fd.Name.Name] = fd
			}
		}
	}
	if len(storeMethods) == 0 {
		t.Fatal("parsed zero *Store methods — the gate cannot run")
	}

	// recvName returns the receiver identifier of a *Store method
	// (conventionally "s", but read it rather than assume).
	recvName := func(fd *ast.FuncDecl) string {
		if fd.Recv != nil && len(fd.Recv.List) == 1 && len(fd.Recv.List[0].Names) == 1 {
			return fd.Recv.List[0].Names[0].Name
		}
		return ""
	}

	// writerOps are the *sql.DB operations that, when invoked on the
	// writer pool (`<recv>.db`), make a method a writer-connection user.
	writerOps := map[string]bool{
		"Query": true, "QueryRow": true, "QueryContext": true, "QueryRowContext": true,
		"Exec": true, "ExecContext": true, "Begin": true, "BeginTx": true,
		"Prepare": true, "PrepareContext": true,
	}

	// scan walks fd's body: returns whether it invokes a writer op on
	// `<recv>.db`, and the set of *Store methods it calls on its
	// receiver (for transitive descent). A bare `<recv>.db` reference
	// that is NOT a query/exec/begin call — e.g. RO()'s defensive
	// `return s.db` fallback — is intentionally not flagged.
	scan := func(fd *ast.FuncDecl) (touchesDB bool, calls []string) {
		recv := recvName(fd)
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			// (a) transitive descent: <recv>.<storeMethod>
			if base, ok := sel.X.(*ast.Ident); ok && base.Name == recv {
				if _, isMethod := storeMethods[sel.Sel.Name]; isMethod {
					calls = append(calls, sel.Sel.Name)
				}
			}
			// (b) writer-connection use: <recv>.db.<writerOp>
			if writerOps[sel.Sel.Name] {
				if inner, ok := sel.X.(*ast.SelectorExpr); ok && inner.Sel.Name == "db" {
					if base, ok := inner.X.(*ast.Ident); ok && base.Name == recv {
						touchesDB = true
					}
				}
			}
			return true
		})
		return touchesDB, calls
	}

	var failures []string
	names := make([]string, 0, len(readerRoutedStoreMethods))
	for n := range readerRoutedStoreMethods {
		names = append(names, n)
	}
	sort.Strings(names)

	for _, name := range names {
		root := storeMethods[name]
		if root == nil {
			// Classified but not found as a *Store method in package db
			// — TestStore_AllExportedMethodsClassified owns that check.
			continue
		}
		visited := map[string]bool{}
		var offender string
		var walk func(methodName string, chain []string) bool
		walk = func(methodName string, chain []string) bool {
			if visited[methodName] {
				return false
			}
			visited[methodName] = true
			fd := storeMethods[methodName]
			if fd == nil {
				return false
			}
			touchesDB, calls := scan(fd)
			here := append(chain, methodName)
			if touchesDB {
				offender = strings.Join(here, " -> ")
				return true
			}
			for _, c := range calls {
				if walk(c, here) {
					return true
				}
			}
			return false
		}
		if walk(name, nil) {
			failures = append(failures, "  "+offender+"  (reader-classified, reaches the writer connection)")
		}
	}

	if len(failures) > 0 {
		t.Errorf("%d reader-routed method(s) touch s.db — they must use s.ro (the reader pool):\n%s\n\n"+
			"Either route the SELECT through s.ro, or, if the method genuinely writes, move it to "+
			"writerRoutedStoreMethods in db_test.go. See #1824/#1826.",
			len(failures), strings.Join(failures, "\n"))
	}
}
