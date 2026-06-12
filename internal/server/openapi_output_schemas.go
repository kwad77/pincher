// SPDX-License-Identifier: MIT

package server

import "encoding/json"

// outputSchemaJSON returns the JSON Schema for each tool's success
// response body (#581). Lifted to its own file so the per-tool
// payload — 24 schemas, ~5-15 fields each — doesn't bloat
// registerTools or server.go's main flow. The wireToolOutputSchemas
// helper threads them onto s.outputSchemas after registerTools so
// the OpenAPI spec carries a real contract per endpoint instead of
// the bare {type: object} placeholder.
//
// Each schema describes the success-path response only. The shared
// _meta envelope is referenced via $ref to the Meta component
// declared in openAPIComponentSchemas. Errors fall through to the
// `default` response with $ref to Error.
//
// When adding a new tool, its OutputSchema MUST be declared here OR
// the TestOpenAPI_EveryToolHasNonPlaceholderOutputSchema gate fails
// CI (sibling of the request-side TestOpenAPI_PerToolSchemaIsRealNotPlaceholder
// from #560).
func outputSchemaJSON(name string) json.RawMessage {
	if s, ok := outputSchemas[name]; ok {
		return json.RawMessage(s)
	}
	return nil
}

// metaRef is the $ref to the shared _meta envelope component.
// Inlined as a string to keep the per-tool schema declarations
// compact.
const metaRef = `{"$ref":"#/components/schemas/Meta"}`

var outputSchemas = map[string]string{
	// 1. index — write-side; returns counts.
	"index": `{
		"type":"object",
		"required":["project","files","symbols","edges"],
		"properties":{
			"project":{"type":"string"},
			"path":{"type":"string"},
			"files":{"type":"integer"},
			"symbols":{"type":"integer"},
			"edges":{"type":"integer"},
			"deleted":{"type":"integer"},
			"skipped":{"type":"integer"},
			"blocked":{"type":"integer"},
			"duration_ms":{"type":"integer"},
			"_meta":` + metaRef + `
		}
	}`,

	// 2. symbol — read by ID.
	"symbol": `{
		"type":"object",
		"required":["id","name","kind","language","file_path"],
		"properties":{
			"id":{"type":"string"},
			"name":{"type":"string"},
			"qualified_name":{"type":"string"},
			"kind":{"type":"string"},
			"language":{"type":"string"},
			"file_path":{"type":"string"},
			"start_line":{"type":"integer"},
			"end_line":{"type":"integer"},
			"start_byte":{"type":"integer"},
			"end_byte":{"type":"integer"},
			"signature":{"type":"string"},
			"docstring":{"type":"string"},
			"return_type":{"type":"string"},
			"is_exported":{"type":"boolean"},
			"is_test":{"type":"boolean"},
			"complexity":{"type":"integer"},
			"extraction_confidence":{"type":"number"},
			"source":{"type":"string"},
			"_meta":` + metaRef + `
		}
	}`,

	// 3. symbols — batch read.
	"symbols": `{
		"type":"object",
		"required":["symbols","_meta"],
		"properties":{
			"symbols":{"type":"array","items":{"type":"object"}},
			"missing":{"type":"array","items":{"type":"string"}},
			"_meta":` + metaRef + `
		}
	}`,

	// 4. context — symbol + imports.
	"context": `{
		"type":"object",
		"required":["symbol","_meta"],
		"properties":{
			"symbol":{"type":"object"},
			"imports":{"type":"array","items":{"type":"object"}},
			"_meta":` + metaRef + `
		}
	}`,

	// 5. search — BM25-ranked. v0.25 #532: pagination via limit/offset
	// with total/has_more in the response envelope.
	// `results` is required unless format=text/toon was passed, in
	// which case `results_text`/`results_toon` replaces it — so none
	// of the three is in `required`.
	// count_only=true (conclusion-density) returns only
	// {query, total, by_kind, _meta}, so the row/pagination fields are
	// present on the default shape but not required by the schema.
	"search": `{
		"type":"object",
		"required":["query","total","_meta"],
		"properties":{
			"query":{"type":"string"},
			"by_kind":{"type":"object","additionalProperties":{"type":"integer"},"description":"count_only=true only: post-filter match count per symbol kind."},
			"count":{"type":"integer","description":"Number of results in this page (len(results))."},
			"total":{"type":"integer","description":"Total post-filter result count considered. Lower bound when has_more is true and the FTS5 fetch hit the cap (5000)."},
			"has_more":{"type":"boolean","description":"True when there's at least one row past offset+limit. Drives the dashboard's Load more button."},
			"offset":{"type":"integer","description":"The offset that produced this page (echoed for clients)."},
			"limit":{"type":"integer","description":"The limit that produced this page (echoed for clients)."},
			"results":{"type":"array","items":{
				"type":"object",
				"properties":{
					"id":{"type":"string"},
					"name":{"type":"string"},
					"qualified_name":{"type":"string"},
					"kind":{"type":"string"},
					"language":{"type":"string"},
					"file_path":{"type":"string"},
					"start_line":{"type":"integer"},
					"end_line":{"type":"integer"},
					"start_byte":{"type":"integer"},
					"end_byte":{"type":"integer"},
					"signature":{"type":"string"},
					"snippet":{"type":"string"},
					"score":{"type":"number"},
					"extraction_confidence":{"type":"number"}
				}
			}},
			"results_text":{"type":"string","description":"format=text rendering: TSV block replacing the results array — header row, then one line per hit (id<TAB>kind<TAB>file:line<TAB>signature-or-name)."},
			"results_toon":{"type":"string","description":"format=toon rendering: TOON (Token-Oriented Object Notation) tabular block replacing the results array — results[N]{file,id,kind,line,name,signature}: header, then one bare comma-delimited row per hit (strings quoted only when needed)."},
			"_meta":` + metaRef + `
		}
	}`,

	// 6. query — pinchQL.
	"query": `{
		"type":"object",
		"required":["columns","rows","total","_meta"],
		"properties":{
			"columns":{"type":"array","items":{"type":"string"}},
			"rows":{"type":"array","items":{"type":"object"}},
			"total":{"type":"integer"},
			"warnings":{"type":"array","items":{"type":"string"}},
			"_meta":` + metaRef + `
		}
	}`,

	// 7. trace — graph BFS.
	// `hops` is required unless format=text/toon was passed, in which
	// case `results_text`/`results_toon` replaces it — so none of the
	// three is in `required`.
	// count_only=true (conclusion-density) returns only
	// {root, direction, total, by_depth, by_risk, _meta}, so hops is
	// present on the default shape but not required by the schema.
	"trace": `{
		"type":"object",
		"required":["root","direction","total","_meta"],
		"properties":{
			"root":{"type":"string"},
			"direction":{"type":"string","enum":["inbound","outbound","both"]},
			"hops":{"type":"array","items":{"type":"object"}},
			"results_text":{"type":"string","description":"format=text rendering: TSV block replacing the hops array — header row, then one line per hop (depth<TAB>risk<TAB>id)."},
			"results_toon":{"type":"string","description":"format=toon rendering: TOON (Token-Oriented Object Notation) tabular block replacing the hops array — hops[N]{depth,id,risk}: header, then one bare comma-delimited row per hop."},
			"total":{"type":"integer"},
			"by_depth":{"type":"object","additionalProperties":{"type":"integer"},"description":"count_only=true only: hop count per BFS depth."},
			"by_risk":{"type":"object","additionalProperties":{"type":"integer"},"description":"count_only=true only (risk=true): hop count per risk label."},
			"risk_summary":{"type":"object","properties":{
				"CRITICAL":{"type":"integer"},
				"HIGH":{"type":"integer"},
				"MEDIUM":{"type":"integer"},
				"LOW":{"type":"integer"}
			}},
			"_meta":` + metaRef + `
		}
	}`,

	// 8. changes — git-diff blast radius.
	"changes": `{
		"type":"object",
		"required":["changed_files","changed_symbols","impacted","summary","tests_to_run","_meta"],
		"properties":{
			"changed_files":{"type":"array","items":{"type":"string"}},
			"changed_symbols":{"type":"array","items":{"type":"object"}},
			"impacted":{"type":"array","items":{"type":"object"}},
			"summary":{"type":"object","properties":{
				"changed_files":{"type":"integer"},
				"changed_symbols":{"type":"integer"},
				"total_impacted":{"type":"integer"},
				"critical":{"type":"integer"},
				"high":{"type":"integer"},
				"medium":{"type":"integer"},
				"low":{"type":"integer"},
				"tests_to_run":{"type":"integer"}
			}},
			"tests_to_run":{"type":"array","items":{"type":"object"}},
			"_meta":` + metaRef + `
		}
	}`,

	// 9. dead_code — unreachable internal symbols.
	"dead_code": `{
		"type":"object",
		"required":["dead_symbols","filters","total","_meta"],
		"properties":{
			"dead_symbols":{"type":"array","items":{
				"type":"object",
				"properties":{
					"id":{"type":"string"},
					"name":{"type":"string"},
					"kind":{"type":"string"},
					"language":{"type":"string"},
					"file_path":{"type":"string"},
					"start_line":{"type":"integer"},
					"complexity":{"type":"integer"}
				}
			}},
			"filters":{"type":"object"},
			"total":{"type":"integer"},
			"_meta":` + metaRef + `
		}
	}`,

	// 10. architecture — orientation.
	"architecture": `{
		"type":"object",
		"required":["project","languages","node_kinds","edge_kinds","entry_points","hotspots","_meta"],
		"properties":{
			"project":{"type":"object"},
			"languages":{"type":"object"},
			"node_kinds":{"type":"object"},
			"edge_kinds":{"type":"object"},
			"entry_points":{"type":"array","items":{"type":"object"}},
			"hotspots":{"type":"array","items":{"type":"object"}},
			"_meta":` + metaRef + `
		}
	}`,

	// 10b. branch_overlap — merge-order risk between two branches.
	"branch_overlap": `{
		"type":"object",
		"required":["branch_a","branch_b","base","overlapping_files","overlapping_symbols","verdict","_meta"],
		"properties":{
			"branch_a":{"type":"string"},
			"branch_b":{"type":"string"},
			"base":{"type":"string"},
			"branch_a_file_count":{"type":"integer"},
			"branch_b_file_count":{"type":"integer"},
			"overlapping_files":{"type":"array","items":{"type":"string"}},
			"overlapping_symbols":{"type":"array","items":{"type":"string"}},
			"verdict":{"type":"string"},
			"_meta":` + metaRef + `
		}
	}`,

	// 10c. assert_graph — conclusion-density: server-side invariant
	// evaluation over the edge graph. violations only present on fail.
	"assert_graph": `{
		"type":"object",
		"required":["kind","target","pass","checked","_meta"],
		"properties":{
			"kind":{"type":"string","enum":["no_callers_outside","max_callers","no_calls_to","exists"]},
			"target":{"type":"string","description":"The resolved symbol id (caller-shaped kinds) or the input target (exists)."},
			"pass":{"type":"boolean"},
			"checked":{"type":"integer","description":"Direct callers examined (caller-shaped kinds) or matches found (exists)."},
			"violations":{"type":"array","items":{"type":"object","properties":{
				"id":{"type":"string"},
				"file_path":{"type":"string"}
			}},"description":"Only present when pass=false; capped at 10."},
			"violations_total":{"type":"integer","description":"Only present when pass=false; the uncapped violation count."},
			"_meta":` + metaRef + `
		}
	}`,

	// 10d. coach — priced findings mined from recorded usage telemetry.
	"coach": `{
		"type":"object",
		"required":["window","calls_analyzed","findings","_meta"],
		"properties":{
			"window":{"type":"string"},
			"calls_analyzed":{"type":"integer"},
			"findings":{"type":"array","items":{"type":"object","properties":{
				"pattern":{"type":"string"},
				"occurrences":{"type":"integer"},
				"est_tokens_left_on_table":{"type":"integer"},
				"recommendation":{"type":"string"},
				"basis":{"type":"string"}
			}}},
			"note":{"type":"string"},
			"routing":{"type":"object","description":"Routing adoption section (router-loop plan §A4). Present ONLY when a live pincher-router was detected at startup — absent-router responses are byte-identical to the pre-routing shape. Carries route_tool_calls, task_spawns_observed, advise_route_advisories, route_consult_coverage (and, session window only, the route_consults/outcome_reports live-counter split) plus a basis string naming every approximation.","properties":{
				"route_tool_calls":{"type":"integer"},
				"route_consults":{"type":"integer"},
				"outcome_reports":{"type":"integer"},
				"task_spawns_observed":{"type":"integer"},
				"advise_route_advisories":{"type":"integer"},
				"route_consult_coverage":{"type":"number"},
				"basis":{"type":"string"}
			}},
			"_meta":` + metaRef + `
		}
	}`,

	// 11. schema — schema diagram.
	"schema": `{
		"type":"object",
		"required":["node_kinds","edge_kinds","_meta"],
		"properties":{
			"node_kinds":{"type":"object"},
			"edge_kinds":{"type":"object"},
			"total_nodes":{"type":"integer"},
			"total_edges":{"type":"integer"},
			"_meta":` + metaRef + `
		}
	}`,

	// 12. list — projects.
	"list": `{
		"type":"object",
		"required":["projects","count","_meta"],
		"properties":{
			"projects":{"type":"array","items":{"type":"object"}},
			"count":{"type":"integer"},
			"filtered_out":{"type":"integer"},
			"filtered_breakdown":{"type":"object","properties":{
				"dead_path":{"type":"integer"},
				"inactive":{"type":"integer"},
				"low_edges":{"type":"integer"}
			}},
			"page":{"type":"object"},
			"pruned":{"type":"array","items":{"type":"string"}},
			"_meta":` + metaRef + `
		}
	}`,

	// 13. adr — persistent decisions store.
	"adr": `{
		"type":"object",
		"properties":{
			"key":{"type":"string"},
			"value":{"type":"string"},
			"entries":{"type":"object","additionalProperties":{"type":"string"}},
			"deleted":{"type":"boolean"},
			"_meta":` + metaRef + `
		}
	}`,

	// 13b. loop — ledger ops return {loop, seq, watermark, receipt};
	// list returns {loops} or {loop, checkpoints}; resume returns the
	// bounded brief; handoff returns {receipt, manifest} (M17); export
	// returns {markdown, from_seq, to_seq}.
	"loop": `{
		"type":"object",
		"properties":{
			"loop":{"type":"string"},
			"seq":{"type":"integer"},
			"watermark":{"type":"string"},
			"receipt":{"type":"string"},
			"manifest":{"type":"string"},
			"markdown":{"type":"string"},
			"from_seq":{"type":"integer"},
			"to_seq":{"type":"integer"},
			"loops":{"type":"array","items":{"type":"object"}},
			"checkpoints":{"type":"array","items":{"type":"object"}},
			"brief":{"type":"array","items":{"type":"object"}},
			"open_triggers":{"type":"array","items":{"type":"object"}},
			"adr_keys":{"type":"array","items":{"type":"string"}},
			"watermark_now":{"type":"string"},
			"index_changed_since_last_checkpoint":{"type":"boolean"},
			"omitted_checkpoints":{"type":"integer"},
			"_meta":` + metaRef + `
		}
	}`,

	// 13c. batch — one envelope, N read-only sub-query answers
	// (loop-substrate). results entries are {index, tool, result, _meta}
	// on success, {index, tool, error} on isolated sub-error, or
	// {index, tool, skipped} once the shared budget is exhausted.
	"batch": `{
		"type":"object",
		"required":["results","count","budget"],
		"properties":{
			"results":{"type":"array","items":{"type":"object"}},
			"count":{"type":"integer"},
			"budget":{"type":"object","properties":{
				"max_tokens":{"type":"integer"},
				"spent_approx":{"type":"integer"},
				"skipped_queries":{"type":"integer"}
			}},
			"_meta":` + metaRef + `
		}
	}`,

	// 14. health — extraction quality + drift.
	"health": `{
		"type":"object",
		"required":["schema_version","db_path","_meta"],
		"properties":{
			"schema_version":{"type":"integer"},
			"db_path":{"type":"string"},
			"project":{"type":"object"},
			"extraction_coverage":{"type":"array","items":{"type":"object"}},
			"binary_stale":{"type":"boolean"},
			"binary_stale_message":{"type":"string"},
			"index_drift":{"type":"boolean"},
			"index_drift_message":{"type":"string"},
			"_meta":` + metaRef + `
		}
	}`,

	// 15. stats — savings counter.
	"stats": `{
		"type":"object",
		"required":["_meta"],
		"properties":{
			"session":{"type":"object"},
			"all_time":{"type":"object"},
			"project":{"type":"object"},
			"_meta":` + metaRef + `
		}
	}`,

	// 16. fetch — external URL → Document.
	"fetch": `{
		"type":"object",
		"required":["id","url","stored","_meta"],
		"properties":{
			"id":{"type":"string"},
			"url":{"type":"string"},
			"title":{"type":"string"},
			"text":{"type":"string"},
			"raw_bytes":{"type":"integer"},
			"stored":{"type":"boolean"},
			"_meta":` + metaRef + `
		}
	}`,

	// 17. neighborhood — same-file symbols.
	"neighborhood": `{
		"type":"object",
		"required":["seed_id","file_path","language","neighbors","count","_meta"],
		"properties":{
			"seed_id":{"type":"string"},
			"file_path":{"type":"string"},
			"language":{"type":"string"},
			"count":{"type":"integer"},
			"neighbors":{"type":"array","items":{"type":"object"}},
			"page":{"type":"object"},
			"_meta":` + metaRef + `
		}
	}`,

	// 18. guide — task → recommended tools.
	"guide": `{
		"type":"object",
		"required":["task","shape","recommended_next_tools","_meta"],
		"properties":{
			"task":{"type":"string"},
			"hint":{"type":"string"},
			"shape":{"type":"string"},
			"recommended_next_tools":{"type":"array","items":{
				"type":"object",
				"properties":{
					"tool":{"type":"string"},
					"args":{"type":"string"},
					"why":{"type":"string"}
				}
			}},
			"_meta":` + metaRef + `
		}
	}`,

	// 16b. context_for_task — #1259 composite of search + context + trace + changes.
	"context_for_task": `{
		"type":"object",
		"required":["seeds","neighbors","callers","callees","recent_changes","_meta"],
		"properties":{
			"task":{"type":"string"},
			"seed_id":{"type":"string"},
			"seeds":{"type":"array","items":{
				"type":"object",
				"properties":{
					"id":{"type":"string"},
					"name":{"type":"string"},
					"qualified_name":{"type":"string"},
					"kind":{"type":"string"},
					"file_path":{"type":"string"},
					"start_line":{"type":"integer"},
					"end_line":{"type":"integer"},
					"signature":{"type":"string"},
					"score":{"type":"number"}
				}
			}},
			"neighbors":{"type":"array","items":{
				"type":"object",
				"properties":{
					"via_seed":{"type":"string"},
					"id":{"type":"string"},
					"name":{"type":"string"},
					"qualified_name":{"type":"string"},
					"kind":{"type":"string"},
					"file_path":{"type":"string"},
					"start_line":{"type":"integer"}
				}
			}},
			"callers":{"type":"array","items":{
				"type":"object",
				"properties":{
					"via_seed":{"type":"string"},
					"id":{"type":"string"},
					"name":{"type":"string"},
					"qualified_name":{"type":"string"},
					"kind":{"type":"string"},
					"file_path":{"type":"string"},
					"depth":{"type":"integer"},
					"via_kind":{"type":"string"}
				}
			}},
			"callees":{"type":"array","items":{
				"type":"object",
				"properties":{
					"via_seed":{"type":"string"},
					"id":{"type":"string"},
					"name":{"type":"string"},
					"qualified_name":{"type":"string"},
					"kind":{"type":"string"},
					"file_path":{"type":"string"},
					"depth":{"type":"integer"},
					"via_kind":{"type":"string"}
				}
			}},
			"recent_changes":{"type":"array","items":{
				"type":"object",
				"properties":{
					"file_path":{"type":"string"},
					"hunks":{"type":"integer"}
				}
			}},
			"_meta":` + metaRef + `
		}
	}`,

	// 16d. plan_change — #1391 Phase 4 composite #2 (v0.82).
	// Pre-edit blast-radius composite: resolves target, traces inbound
	// callers, partitions by package boundary + test files, surfaces
	// related ADRs.
	"plan_change": `{
		"type":"object",
		"required":["target","blast_radius","related_adrs","_meta"],
		"properties":{
			"target":{
				"type":"object",
				"properties":{
					"file":{"type":"string"},
					"resolution_path":{"type":"string"},
					"symbols_affected":{"type":"array","items":{"type":"object"}}
				}
			},
			"blast_radius":{
				"type":"object",
				"properties":{
					"depth_1_callers":{"type":"array","items":{"type":"object"}},
					"depth_2_callers":{"type":"array","items":{"type":"object"}},
					"cross_package":{"type":"array","items":{"type":"object"}},
					"test_files_intersecting":{"type":"array","items":{"type":"string"}},
					"summary":{
						"type":"object",
						"properties":{
							"depth_1_count":{"type":"integer"},
							"depth_2_count":{"type":"integer"},
							"cross_package_count":{"type":"integer"},
							"test_file_count":{"type":"integer"}
						}
					}
				}
			},
			"related_adrs":{"type":"array","items":{
				"type":"object",
				"properties":{
					"key":{"type":"string"},
					"value":{"type":"string"},
					"why":{"type":"string"}
				}
			}},
			"_meta":` + metaRef + `
		}
	}`,

	// 16h. verify_change — loop-substrate PR-10. The post-edit gate:
	// changes analysis + ranked tests + predicted-vs-actual plan
	// comparison + possibly-orphaned check, one envelope.
	// plan_comparison is OPTIONAL — present only when `target` was
	// passed AND a plan_change run is cached for it.
	"verify_change": `{
		"type":"object",
		"required":["summary","changed_symbols","tests_to_run","possibly_orphaned","_meta"],
		"properties":{
			"summary":{
				"type":"object",
				"properties":{
					"changed_files":{"type":"integer"},
					"changed_symbols":{"type":"integer"},
					"total_impacted":{"type":"integer"},
					"tests_to_run":{"type":"integer"},
					"critical":{"type":"integer"},
					"high":{"type":"integer"},
					"possibly_orphaned":{"type":"integer"}
				}
			},
			"changed_symbols":{"type":"array","items":{"type":"object"}},
			"tests_to_run":{"type":"array","items":{
				"type":"object",
				"properties":{
					"id":{"type":"string"},
					"name":{"type":"string"},
					"file_path":{"type":"string"},
					"overlap":{"type":"integer"}
				}
			}},
			"plan_comparison":{
				"type":"object",
				"properties":{
					"target":{"type":"string"},
					"predicted_callers":{"type":"array","items":{"type":"string"}},
					"actual_impacted":{"type":"array","items":{"type":"string"}},
					"unpredicted_impact":{"type":"array","items":{"type":"string"}},
					"stale":{"type":"boolean"},
					"target_file_in_diff":{"type":"boolean"}
				}
			},
			"possibly_orphaned":{"type":"array","items":{
				"type":"object",
				"properties":{
					"id":{"type":"string"},
					"name":{"type":"string"},
					"kind":{"type":"string"},
					"file_path":{"type":"string"},
					"label":{"type":"string"}
				}
			}},
			"_meta":` + metaRef + `
		}
	}`,

	// 16e. audit_unused — #1391 Phase 4 composite #3 (v0.83).
	// Dead-code with deep-trace confirmation: runs dead_code, then per
	// candidate fires a scoped inbound CALLS trace, classifies each by
	// what the trace surfaced.
	"audit_unused": `{
		"type":"object",
		"required":["candidates","summary","_meta"],
		"properties":{
			"candidates":{"type":"array","items":{
				"type":"object",
				"required":["symbol_id","name","qualified_name","kind","file_path","language","confidence","trace_summary","evidence"],
				"properties":{
					"symbol_id":{"type":"string"},
					"name":{"type":"string"},
					"qualified_name":{"type":"string"},
					"kind":{"type":"string"},
					"file_path":{"type":"string"},
					"start_line":{"type":"integer"},
					"language":{"type":"string"},
					"confidence":{"type":"string","description":"high | medium | low"},
					"trace_summary":{"type":"object"},
					"evidence":{"type":"array","items":{"type":"string"}}
				}
			}},
			"summary":{
				"type":"object",
				"required":["candidates_audited","deep_trace_confirmed_unused","deep_trace_surfaced_dynamic_callers","deep_trace_surfaced_direct_callers"],
				"properties":{
					"candidates_audited":{"type":"integer"},
					"deep_trace_confirmed_unused":{"type":"integer"},
					"deep_trace_surfaced_dynamic_callers":{"type":"integer"},
					"deep_trace_surfaced_direct_callers":{"type":"integer"}
				}
			},
			"_meta":` + metaRef + `
		}
	}`,

	// 16f. onboard_module — #1391 Phase 4 composite #4 (v0.84).
	// New-contributor orientation: scope-scan + entry-point + boundary edges.
	"onboard_module": `{
		"type":"object",
		"required":["scope","entry_points_local_to_scope","external_dependencies","external_consumers","module_summary","_meta"],
		"properties":{
			"scope":{
				"type":"object",
				"required":["directory","file_count","symbol_count"],
				"properties":{
					"directory":{"type":"string"},
					"file_count":{"type":"integer"},
					"symbol_count":{"type":"integer"}
				}
			},
			"entry_points_local_to_scope":{"type":"array","items":{"type":"object"}},
			"external_dependencies":{"type":"array","items":{"type":"object"}},
			"external_consumers":{"type":"array","items":{"type":"object"}},
			"module_summary":{
				"type":"object",
				"required":["language_breakdown","test_to_code_ratio","exported_surface_count","entry_point_count"],
				"properties":{
					"language_breakdown":{"type":"object"},
					"test_to_code_ratio":{"type":"number"},
					"exported_surface_count":{"type":"integer"},
					"entry_point_count":{"type":"integer"}
				}
			},
			"_meta":` + metaRef + `
		}
	}`,

	// 16g. why_empty — #1391 Phase 4 composite #5 (v0.85).
	// Stateless catalog lookup for empty-result recovery.
	"why_empty": `{
		"type":"object",
		"required":["empty_reason","title","when_it_fires","recovery_action","recovery_steps","catalog_anchor","_meta"],
		"properties":{
			"empty_reason":{"type":"string"},
			"title":{"type":"string"},
			"when_it_fires":{"type":"string"},
			"recovery_action":{"type":"string"},
			"recovery_steps":{"type":"array","items":{"type":"object","properties":{
				"tool":{"type":"string"},
				"args":{"type":"string"},
				"why":{"type":"string"}
			}}},
			"catalog_anchor":{"type":"string"},
			"_meta":` + metaRef + `
		}
	}`,

	// 16c. investigate_failure — #1391 Phase 4 composite #1 (v0.81).
	// Bug-hunt composite: parses stack trace, ranks suspects, unions
	// callers, intersects recent changes.
	"investigate_failure": `{
		"type":"object",
		"required":["implicated_symbols","callers","recent_changes","rank","frames_parsed","_meta"],
		"properties":{
			"error_text":{"type":"string"},
			"implicated_symbols":{"type":"array","items":{
				"type":"object",
				"properties":{
					"symbol_id":{"type":"string"},
					"name":{"type":"string"},
					"qualified_name":{"type":"string"},
					"kind":{"type":"string"},
					"file_path":{"type":"string"},
					"start_line":{"type":"integer"},
					"end_line":{"type":"integer"},
					"signature":{"type":"string"},
					"score":{"type":"number"},
					"evidence":{"type":"array","items":{"type":"string"}},
					"stack_frame_match":{"type":"string"},
					"caller_fan_in":{"type":"integer"},
					"recent_change_file":{"type":"boolean"}
				}
			}},
			"callers":{"type":"array","items":{
				"type":"object",
				"properties":{
					"via_suspect":{"type":"string"},
					"id":{"type":"string"},
					"name":{"type":"string"},
					"qualified_name":{"type":"string"},
					"kind":{"type":"string"},
					"file_path":{"type":"string"},
					"depth":{"type":"integer"},
					"via_kind":{"type":"string"}
				}
			}},
			"recent_changes":{"type":"array","items":{
				"type":"object",
				"properties":{
					"file_path":{"type":"string"}
				}
			}},
			"rank":{"type":"array","items":{"type":"object"}},
			"frames_parsed":{
				"type":"object",
				"properties":{
					"names":{"type":"array","items":{"type":"string"}},
					"files":{"type":"array","items":{"type":"string"}}
				}
			},
			"_meta":` + metaRef + `
		}
	}`,

	// 19. init — inject pincher policy into editor rules.
	"init": `{
		"type":"object",
		"required":["target","action","_meta"],
		"properties":{
			"target":{"type":"string"},
			"action":{"type":"string"},
			"path":{"type":"string"},
			"backup":{"type":"string"},
			"applied":{"type":"boolean"},
			"_meta":` + metaRef + `
		}
	}`,

	// 20. doctor — diagnostic report (#558 phase 2).
	"doctor": `{
		"type":"object",
		"required":["binary_version","schema_version","db_size_bytes","wal_size_bytes","projects","extraction_failures","slow_queries","advisories","_meta"],
		"properties":{
			"binary_version":{"type":"string"},
			"generated_at":{"type":"string"},
			"schema_version":{"type":"integer"},
			"db_size_bytes":{"type":"integer"},
			"wal_size_bytes":{"type":"integer"},
			"lookback_hours":{"type":"integer"},
			"projects":{"type":"array","items":{"type":"object"}},
			"extraction_failures":{"type":"array","items":{"type":"object"}},
			"slow_queries":{"type":"array","items":{"type":"object"}},
			"advisories":{"type":"array","items":{"type":"string"},"description":"Human-readable health advisories — e.g. a pathologically large DB (#732). Empty array when healthy."},
			"_meta":` + metaRef + `
		}
	}`,

	// 21. rebuild_fts — admin (#558 phase 2).
	"rebuild_fts": `{
		"type":"object",
		"required":["dry_run","_meta"],
		"properties":{
			"dry_run":{"type":"boolean"},
			"would_reindex_symbols":{"type":"integer"},
			"rebuilt_rows":{"type":"integer"},
			"duration_ms":{"type":"integer"},
			"hint":{"type":"string"},
			"_meta":` + metaRef + `
		}
	}`,

	// 22. self_test — smoke test (#558 phase 2).
	"self_test": `{
		"type":"object",
		"required":["ok","steps","_meta"],
		"properties":{
			"ok":{"type":"boolean"},
			"steps":{"type":"array","items":{
				"type":"object",
				"properties":{
					"label":{"type":"string"},
					"ok":{"type":"boolean"},
					"duration_ms":{"type":"integer"},
					"error":{"type":"string"}
				}
			}},
			"_meta":` + metaRef + `
		}
	}`,

	// 23. models — router registry render (router-loop B5; conditional
	// MCP advertisement, HTTP route always present). Success path is
	// the router's GET /v1/models body passed through: handshake +
	// providers (+ registry_error when the router has no readable
	// registry, + hint when the handshake is pre-v2).
	"models": `{
		"type":"object",
		"required":["handshake","_meta"],
		"properties":{
			"handshake":{
				"type":"object",
				"description":"Contract-v2 version-skew handshake served by the router.",
				"properties":{
					"contract_version":{"type":"integer"},
					"weights_version":{"type":"string"},
					"registry_version":{"type":["integer","null"]},
					"capabilities":{"type":"array","items":{"type":"string"}}
				}
			},
			"providers":{"type":"object","description":"Registry render keyed by provider; auth is the spec string (e.g. env:ANTHROPIC_API_KEY) — resolved credentials never cross this boundary."},
			"policy":{"type":"object"},
			"originating_model_passthrough":{"type":"boolean"},
			"registry_error":{"type":"string"},
			"hint":{"type":"string","description":"Present when the router's contract_version is below the one this proxy is built against — installed-but-old, upgrade pincher-router."},
			"_meta":` + metaRef + `
		}
	}`,

	// 24. route — mode-tagged ExecutionPlan (action=route) or outcomes
	// ack (action=outcome). Plan fields beyond mode/request_id are the
	// router's ExecutionPlan and pass through verbatim.
	"route": `{
		"type":"object",
		"properties":{
			"mode":{"type":"string","enum":["execute","advise"],"description":"execute = the router owns the work (gate the result); advise = spawn a host subagent at the advised tier with the returned envelope."},
			"request_id":{"type":"string","description":"Correlation id for the outcomes join — report it back via action='outcome' after the gate verdict."},
			"ok":{"type":"boolean","description":"action='outcome' acknowledgement."},
			"_meta":` + metaRef + `
		}
	}`,
}
