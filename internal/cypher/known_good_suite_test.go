// SPDX-License-Identifier: MIT

package cypher

import (
	"context"
	"database/sql"
	"testing"
)

// Known-good cypher query suite — the v0.59 safety net.
//
// Every query in the table below is a real-world pinchQL shape that
// should produce ZERO warnings against a representative seeded DB.
// New warning emitters added to engine.go must keep this list green;
// any new warning that fires on a known-good query is a false positive
// by definition and the gate fails.
//
// Coverage targets: node scans, single-hop joins, BFS variable hops,
// WHERE operators (=, <>, IS NULL, IS NOT NULL, comparison, LIKE,
// regex, NOT prefix, AND/OR, parens), DISTINCT, ORDER BY, LIMIT,
// aggregates (COUNT, COUNT(*), SUM, AVG, MIN, MAX), GROUP BY semantics,
// inline pattern props, all edge directions.

func seedKnownGoodDB(t *testing.T) *sql.DB {
	t.Helper()
	db := newTestDB(t)

	// Symbols — mixed languages, kinds, exported/test/entry flags.
	rows := []struct {
		id, name, kind, lang string
		exported, entry, test int
		complexity           int
		signature            string
		docstring            string
	}{
		{"go_main", "main", "Function", "Go", 0, 1, 0, 3, "func main()", "entry"},
		{"go_init", "init", "Function", "Go", 0, 0, 0, 1, "func init()", ""},
		{"go_handler", "HandleRequest", "Function", "Go", 1, 0, 0, 8, "func HandleRequest(r *Request) error", "doc"},
		{"go_helper", "parsePath", "Function", "Go", 0, 0, 0, 2, "func parsePath(s string) string", ""},
		{"go_open", "Open", "Method", "Go", 1, 0, 0, 4, "func (s *Store) Open() error", "opens store"},
		{"go_close", "Close", "Method", "Go", 1, 0, 0, 2, "func (s *Store) Close() error", "closes store"},
		{"go_test_main", "TestMain", "Function", "Go", 1, 0, 1, 5, "func TestMain(m *testing.M)", ""},
		{"py_main", "main", "Function", "Python", 0, 1, 0, 2, "def main()", "py entry"},
		{"py_helper", "parse_path", "Function", "Python", 0, 0, 0, 1, "def parse_path(s)", ""},
		{"py_class", "Parser", "Class", "Python", 1, 0, 0, 0, "", "parser class"},
		{"var_cfg", "Config", "Variable", "Go", 1, 0, 0, 0, "", ""},
		{"const_max", "MaxRows", "Constant", "Go", 1, 0, 0, 0, "", ""},
	}
	for _, r := range rows {
		_, err := db.Exec(`
			INSERT INTO symbols(id, project_id, file_path, name, qualified_name, kind, language,
				start_byte, end_byte, start_line, end_line, is_exported, is_entry_point, complexity,
				signature, docstring, is_test)
			VALUES (?, 'proj1', ?, ?, ?, ?, ?, 0, 100, 1, 20, ?, ?, ?, ?, ?, ?)`,
			r.id, "file.go", r.name, r.name, r.kind, r.lang,
			r.exported, r.entry, r.complexity, r.signature, r.docstring, r.test,
		)
		if err != nil {
			t.Fatalf("seed %s: %v", r.id, err)
		}
	}

	// Edges — CALLS, READS, WRITES across symbols.
	edges := []struct {
		from, to, kind string
	}{
		{"go_main", "go_handler", "CALLS"},
		{"go_main", "go_open", "CALLS"},
		{"go_handler", "go_helper", "CALLS"},
		{"go_handler", "go_close", "CALLS"},
		{"go_open", "var_cfg", "READS"},
		{"go_open", "const_max", "READS"},
		{"go_close", "var_cfg", "WRITES"},
		{"py_main", "py_helper", "CALLS"},
	}
	for _, e := range edges {
		insertEdge(t, db, e.from, e.to, e.kind)
	}
	return db
}

func TestExecute_KnownGoodSuite_NoWarnings(t *testing.T) {
	db := seedKnownGoodDB(t)
	defer db.Close()

	cases := []struct {
		name  string
		query string
	}{
		// Node scans.
		{"all_functions", `MATCH (n:Function) RETURN n.name`},
		{"functions_by_lang", `MATCH (n:Function) WHERE n.language = "Go" RETURN n.name`},
		{"exported_only", `MATCH (n) WHERE n.is_exported = true RETURN n.name`},
		{"entry_points", `MATCH (n:Function) WHERE n.is_entry_point = true RETURN n.name`},
		{"complexity_threshold", `MATCH (n:Function) WHERE n.complexity > 2 RETURN n.name, n.complexity`},
		{"is_null", `MATCH (n:Function) WHERE n.signature IS NULL RETURN n.name`},
		{"is_not_null", `MATCH (n:Function) WHERE n.docstring IS NOT NULL RETURN n.name`},
		{"not_prefix", `MATCH (n:Function) WHERE NOT n.is_test = true RETURN n.name`},
		{"like_operator", `MATCH (n:Function) WHERE n.name =~ ".*Request.*" RETURN n.name`},
		{"and_or", `MATCH (n:Function) WHERE n.language = "Go" AND n.is_exported = true RETURN n.name`},
		{"or_grouping", `MATCH (n) WHERE (n.kind = "Function" OR n.kind = "Method") AND n.is_exported = true RETURN n.name, n.kind`},

		// Inline pattern props (valid keys).
		{"inline_label_prop", `MATCH (n:Function {language: "Go"}) RETURN n.name`},

		// DISTINCT / ORDER BY / LIMIT.
		{"distinct", `MATCH (n:Function) RETURN DISTINCT n.language`},
		{"order_by_asc", `MATCH (n:Function) RETURN n.name ORDER BY n.name ASC`},
		{"order_by_desc_complexity", `MATCH (n:Function) RETURN n.name, n.complexity ORDER BY n.complexity DESC`},
		{"limit", `MATCH (n:Function) RETURN n.name LIMIT 3`},
		{"order_then_limit", `MATCH (n:Function) RETURN n.name ORDER BY n.name LIMIT 2`},

		// Aggregates.
		{"count_star", `MATCH (n:Function) RETURN COUNT(*)`},
		{"count_var", `MATCH (n:Function) RETURN COUNT(n.id)`},
		{"count_group_by", `MATCH (n:Function) RETURN n.language, COUNT(*)`},
		{"order_by_count_star", `MATCH (n:Function) RETURN n.language, COUNT(*) ORDER BY COUNT(*) DESC`},
		{"min_max_int", `MATCH (n:Function) RETURN MIN(n.complexity), MAX(n.complexity)`},
		{"sum_avg_int", `MATCH (n:Function) RETURN SUM(n.complexity), AVG(n.complexity)`},

		// Single-hop joins (outbound is the supported direction).
		{"outbound_calls", `MATCH (a:Function)-[:CALLS]->(b) RETURN a.name, b.name`},
		{"reads_edge", `MATCH (a:Function)-[:READS]->(b) RETURN a.name, b.name`},
		{"writes_edge", `MATCH (a:Method)-[:WRITES]->(b) RETURN a.name, b.name`},

		// Variable-length BFS hops.
		{"one_hop_explicit", `MATCH (a:Function)-[:CALLS*1..1]->(b) RETURN a.name, b.name`},
		{"two_hop_path", `MATCH (a:Function)-[:CALLS*1..2]->(b) RETURN a.name, b.name`},

		// Multi-property RETURN.
		{"multi_props", `MATCH (n:Function) RETURN n.name, n.language, n.kind, n.complexity`},

		// Edge filter with predicate on the destination.
		{"join_with_where", `MATCH (a:Function)-[:CALLS]->(b:Function) WHERE b.name = "HandleRequest" RETURN a.name, b.name`},

		// Comparison + AND on numeric column.
		{"numeric_range", `MATCH (n:Function) WHERE n.complexity >= 2 AND n.complexity <= 8 RETURN n.name`},

		// Shapes added by v0.59 fixes — these must keep producing
		// zero warnings across all the new emitters (#1123/#1127/
		// #1129/#1133/#1136/#1139/#1140 territory).
		{"aliased_return_with_aliased_orderby", `MATCH (n:Function) RETURN n.name AS title, n.complexity AS cx ORDER BY cx DESC`},
		{"aliased_return_orderby_source", `MATCH (n:Function) RETURN n.name AS title, n.complexity AS cx ORDER BY n.complexity DESC`},
		{"grouped_aggregate_orderby_count_star", `MATCH (n:Function) RETURN n.language, COUNT(*) ORDER BY COUNT(*) DESC`},
		{"distinct_grouped_aggregate", `MATCH (n:Function) RETURN DISTINCT n.language, COUNT(*) ORDER BY COUNT(*) DESC`},
		{"aggregate_min_max_int", `MATCH (n:Function) WHERE n.complexity > 0 RETURN MIN(n.complexity), MAX(n.complexity)`},
		{"edge_with_confidence_predicate", `MATCH (a:Function)-[r:CALLS]->(b) WHERE r.confidence > 0.5 RETURN a.name, r.confidence LIMIT 5`},
		{"valid_kind_in_aggregate_pattern", `MATCH (n:Function) RETURN COUNT(*)`},
		{"return_bare_bound_variable", `MATCH (n:Function) RETURN n LIMIT 1`},
		{"is_null_with_aggregate", `MATCH (n:Function) WHERE n.docstring IS NULL RETURN COUNT(*)`},
		{"is_not_null_with_aggregate", `MATCH (n:Function) WHERE n.docstring IS NOT NULL RETURN COUNT(*)`},
		{"nested_not_or_in_where", `MATCH (n:Function) WHERE NOT (n.is_test = true OR n.is_exported = false) RETURN COUNT(*)`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := &Executor{DB: db, MaxRows: 100, ProjectID: "proj1"}
			r, err := e.Execute(context.Background(), tc.query)
			if err != nil {
				t.Fatalf("Execute(%q): %v", tc.query, err)
			}
			if len(r.Warnings) != 0 {
				t.Errorf("known-good query produced warnings (should be zero).\n  query:    %s\n  warnings: %v",
					tc.query, r.Warnings)
			}
		})
	}
}
