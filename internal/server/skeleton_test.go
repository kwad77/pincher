// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kwad77/pincher/internal/db"
)

// Skeleton mode (detail="skeleton") — deterministic structural compression
// of source bodies. These tests pin:
//   - measured economics: skeleton of a representative ~120-line mixed-flow
//     Go function costs < 30% of the full body's tokens,
//   - the graph guarantee: every CALLS-edge callee name appears in the
//     skeleton output,
//   - determinism: identical input → identical output,
//   - the _meta.skeleton:true marker (one top-level marker per response),
//   - unknown detail values degrade to full source with a warning,
//   - context's #655 diff cache is BYPASSED in skeleton mode — different
//     representations must not poison each other's cache.

// skelFixtureSrc is a representative large Go function: ~120 lines of
// mixed control flow (if / else / for / switch / select / defer / return)
// wrapping runs of straight-line statements that skeleton mode elides.
const skelFixtureSrc = `func ProcessOrder(ctx context.Context, raw []byte, opts Options) (*Receipt, error) {
	var receipt Receipt
	startedAt := time.Now()
	traceID := newTraceID()
	attempt := 0
	maxAttempts := opts.MaxAttempts
	backoff := opts.InitialBackoff
	currency := opts.Currency
	locale := opts.Locale
	region := opts.Region
	warehouse := opts.Warehouse
	clock := opts.Clock
	defer trackDuration(startedAt)
	order, err := parseInput(raw)
	if err != nil {
		wrapped := fmt.Errorf("parse: %w", err)
		metricParseFailures.Inc()
		lastErr = wrapped
		hint := suggestEncoding(raw)
		annotated := annotate(wrapped, hint)
		logEntry := buildLogEntry(traceID, annotated)
		sink.Write(logEntry)
		return nil, annotated
	}
	if order.ID == "" {
		order.ID = newOrderID(region)
		order.CreatedAt = clock.Now()
		order.Locale = locale
		order.Currency = currency
		order.Source = "inline"
		order.Warehouse = warehouse
	}
	for _, line := range order.Lines {
		sku := line.SKU
		qty := line.Quantity
		unit := line.UnitPrice
		discount := line.Discount
		weight := line.Weight
		taxable := line.Taxable
		category := line.Category
		if qty <= 0 {
			continue
		}
		extended := unit * float64(qty)
		discounted := extended * (1 - discount)
		rounded := roundCents(discounted)
		receipt.Subtotal += rounded
		receipt.TotalWeight += weight * float64(qty)
		receipt.LineCount++
		_ = sku
		_ = taxable
		_ = category
	}
	if verr := validateOrder(order); verr != nil {
		retriable := isRetriable(verr)
		metricValidationFailures.Inc()
		detail := describeFailure(verr)
		audit := buildAuditRecord(order.ID, detail)
		stampRegion(&audit, region)
		stampActor(&audit, opts.Actor)
		queueForReview(audit)
		if !retriable {
			return nil, verr
		}
	}
	switch order.Priority {
	case PriorityRush:
		receipt.Surcharge = rushSurcharge(order, region)
		receipt.SLA = slaRush
		receipt.Carrier = pickRushCarrier(warehouse)
	case PriorityBulk:
		receipt.Surcharge = 0
		receipt.SLA = slaBulk
		receipt.Carrier = pickBulkCarrier(warehouse)
		receipt.Pallets = estimatePallets(receipt.TotalWeight)
	default:
		receipt.Surcharge = standardSurcharge(region)
		receipt.SLA = slaStandard
		receipt.Carrier = defaultCarrier
	}
	totals := computeTotals(order, receipt.Subtotal)
	receipt.Tax = totals.Tax
	receipt.Shipping = totals.Shipping
	receipt.GrandTotal = totals.Grand
	receipt.Currency = currency
	receipt.IssuedAt = clock.Now()
	receipt.TraceID = traceID
	receipt.Attempt = attempt
	receipt.Backoff = backoff
	receipt.MaxAttempts = maxAttempts
	receipt.Region = region
	receipt.Locale = locale
	receipt.Warehouse = warehouse
	receipt.TaxRegime = lookupTaxRegime(region)
	receipt.ExchangeRate = lookupRate(currency)
	receipt.RoundingMode = opts.RoundingMode
	receipt.DisplayTotal = formatMoney(totals.Grand, currency, locale)
	receipt.DisplayTax = formatMoney(totals.Tax, currency, locale)
	receipt.DisplayShipping = formatMoney(totals.Shipping, currency, locale)
	receipt.Checksum = checksumReceipt(&receipt)
	receipt.SchemaVersion = receiptSchemaVersion
	receipt.GeneratedBy = buildInfo()
	for attempt < maxAttempts {
		attempt++
		err = persistReceipt(&receipt)
		if err == nil {
			break
		}
		backoff = nextBackoff(backoff)
	}
	select {
	case auditCh <- writeAudit(order, receipt):
	default:
		metricAuditDropped.Inc()
	}
	if opts.Notify {
		payload := renderNotification(receipt, locale)
		compressed := gzipBytes(payload)
		signed := signPayload(compressed, opts.Key)
		envelope := buildEnvelope(signed, order.ID)
		queued := enqueue(envelope)
		_ = queued
		notifyCustomer(order, receipt)
	}
	return &receipt, nil
}
`

// skelFixtureCallees are the outbound CALLS edges seeded for the fixture —
// the in-package helpers the graph knows about. A mix of names that text
// matching can place inside elided runs (parseInput, computeTotals, ...)
// exercises the occurrence-ordered marker path; every one of them must
// appear somewhere in the skeleton output.
var skelFixtureCallees = []string{
	"parseInput", "validateOrder", "computeTotals", "writeAudit", "notifyCustomer",
}

// seedSkeletonFixture writes skelFixtureSrc to a temp project and registers
// the symbol (whole-file span) plus its CALLS edges.
func seedSkeletonFixture(t *testing.T, srv *Server) db.Symbol {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "order.go"), []byte(skelFixtureSrc), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if err := srv.store.UpsertProject(db.Project{ID: "skel", Path: dir, Name: "skel"}); err != nil {
		t.Fatalf("upsert project: %v", err)
	}
	sym := db.Symbol{
		ID:                   "skel::orders.ProcessOrder#Function",
		ProjectID:            "skel",
		FilePath:             "order.go",
		Name:                 "ProcessOrder",
		QualifiedName:        "orders.ProcessOrder",
		Kind:                 "Function",
		Language:             "Go",
		StartByte:            0,
		EndByte:              len(skelFixtureSrc),
		StartLine:            1,
		EndLine:              strings.Count(skelFixtureSrc, "\n") + 1,
		ExtractionConfidence: 1.0,
	}
	syms := []db.Symbol{sym}
	edges := make([]db.Edge, 0, len(skelFixtureCallees))
	for _, callee := range skelFixtureCallees {
		calleeSym := db.Symbol{
			ID: "skel::orders." + callee + "#Function", ProjectID: "skel",
			FilePath: "helpers.go", Name: callee,
			QualifiedName: "orders." + callee, Kind: "Function", Language: "Go",
			ExtractionConfidence: 1.0,
		}
		syms = append(syms, calleeSym)
		edges = append(edges, db.Edge{
			ProjectID: "skel", FromID: sym.ID, ToID: calleeSym.ID, Kind: "CALLS",
		})
	}
	if err := srv.store.BulkUpsertSymbols(syms); err != nil {
		t.Fatalf("upsert symbols: %v", err)
	}
	if err := srv.store.BulkUpsertEdges(edges); err != nil {
		t.Fatalf("upsert edges: %v", err)
	}
	return sym
}

func symbolSourceWithDetail(t *testing.T, srv *Server, id, detail string) (string, map[string]any) {
	t.Helper()
	args := map[string]any{"id": id, "project": "skel"}
	if detail != "" {
		args["detail"] = detail
	}
	res, err := srv.handleSymbol(context.Background(), makeReq(args))
	if err != nil {
		t.Fatalf("handleSymbol(detail=%q): %v", detail, err)
	}
	body := decode(t, res)
	src, _ := body["source"].(string)
	return src, body
}

// The measured-economics gate: the fixture's skeleton must cost less than
// 30% of the full body's tokens, AND every CALLS-edge callee name must
// appear in the skeleton.
func TestSymbol_DetailSkeleton_CompressionAndCalleeGuarantee(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	sym := seedSkeletonFixture(t, srv)

	full, _ := symbolSourceWithDetail(t, srv, sym.ID, "full")
	if full != skelFixtureSrc {
		t.Fatalf("detail=full must return verbatim source; got %d bytes want %d", len(full), len(skelFixtureSrc))
	}
	skel, body := symbolSourceWithDetail(t, srv, sym.ID, "skeleton")
	if skel == "" {
		t.Fatal("skeleton source is empty")
	}

	fullTok := db.ApproxTokens(full)
	skelTok := db.ApproxTokens(skel)
	t.Logf("fixture compression: full=%d tokens, skeleton=%d tokens, ratio=%.3f (%d → %d lines)",
		fullTok, skelTok, float64(skelTok)/float64(fullTok),
		strings.Count(full, "\n")+1, strings.Count(skel, "\n")+1)
	if float64(skelTok) >= 0.30*float64(fullTok) {
		t.Errorf("skeleton too large: %d tokens vs %d full (ratio %.3f, want < 0.30)\nSKELETON:\n%s",
			skelTok, fullTok, float64(skelTok)/float64(fullTok), skel)
	}

	for _, callee := range skelFixtureCallees {
		if !containsIdent(skel, callee) {
			t.Errorf("CALLS-edge callee %q missing from skeleton:\n%s", callee, skel)
		}
	}

	// Signature verbatim, elision markers present.
	if !strings.HasPrefix(skel, "func ProcessOrder(ctx context.Context, raw []byte, opts Options) (*Receipt, error) {") {
		t.Errorf("skeleton must start with the verbatim signature; got %q", strings.SplitN(skel, "\n", 2)[0])
	}
	if !strings.Contains(skel, "… ") || !strings.Contains(skel, " lines") {
		t.Errorf("skeleton carries no elision markers:\n%s", skel)
	}

	// _meta.skeleton:true — one top-level marker.
	meta, _ := body["_meta"].(map[string]any)
	if meta == nil || meta["skeleton"] != true {
		t.Errorf("_meta.skeleton=true missing; _meta=%v", body["_meta"])
	}

	// Full responses must NOT carry the marker.
	_, fullBody := symbolSourceWithDetail(t, srv, sym.ID, "full")
	if fullMeta, _ := fullBody["_meta"].(map[string]any); fullMeta != nil {
		if _, present := fullMeta["skeleton"]; present {
			t.Errorf("detail=full must not stamp _meta.skeleton; _meta=%v", fullMeta)
		}
	}
}

// Determinism: identical input → identical output, both at the pure-
// function level and through the handler.
func TestSymbol_DetailSkeleton_Deterministic(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	sym := seedSkeletonFixture(t, srv)

	a, _ := symbolSourceWithDetail(t, srv, sym.ID, "skeleton")
	b, _ := symbolSourceWithDetail(t, srv, sym.ID, "skeleton")
	if a != b {
		t.Errorf("handler skeleton not deterministic:\nA:\n%s\nB:\n%s", a, b)
	}
	if x, y := skeletonize(skelFixtureSrc, skelFixtureCallees), skeletonize(skelFixtureSrc, skelFixtureCallees); x != y {
		t.Errorf("skeletonize not deterministic")
	}
}

// Edge-list fallback: callee names that text matching can't place (e.g.
// aliased or dynamically-dispatched calls) still appear, via the trailing
// "… calls (from graph):" marker.
func TestSkeletonize_GraphCalleeFallback(t *testing.T) {
	t.Parallel()
	src := "func f() {\n\ta := 1\n\tb := helperA(a)\n\t_ = b\n\treturn\n}\n"
	out := skeletonize(src, []string{"helperA", "ghostCallee"})
	if !containsIdent(out, "helperA") {
		t.Errorf("text-matched callee missing:\n%s", out)
	}
	if !strings.Contains(out, "calls (from graph): ghostCallee") {
		t.Errorf("edge-only callee must appear via the graph fallback marker:\n%s", out)
	}
}

// Unknown detail values degrade to full source with a warning — never an
// error, never a silent skeleton.
func TestSymbol_DetailUnknown_WarnsAndReturnsFull(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	sym := seedSkeletonFixture(t, srv)

	src, body := symbolSourceWithDetail(t, srv, sym.ID, "bogus")
	if src != skelFixtureSrc {
		t.Errorf("unknown detail must return full source")
	}
	meta, _ := body["_meta"].(map[string]any)
	if meta == nil {
		t.Fatal("missing _meta")
	}
	warnings, _ := meta["warnings"].([]any)
	found := false
	for _, w := range warnings {
		if s, _ := w.(string); strings.Contains(s, `unknown detail "bogus"`) {
			found = true
		}
	}
	if !found {
		t.Errorf("expected unknown-detail warning; warnings=%v", warnings)
	}
	if _, present := meta["skeleton"]; present {
		t.Errorf("degraded-to-full response must not stamp _meta.skeleton")
	}
}

// Batch shape: every entry's source is skeletonized; ONE top-level
// _meta.skeleton marker (not per-entry — detail applies call-wide).
func TestSymbols_DetailSkeleton_BatchAndTopLevelMarker(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	sym := seedSkeletonFixture(t, srv)

	// The batch's compact default field set excludes source — request it
	// explicitly, as a body-reading caller would.
	res, err := srv.handleSymbols(context.Background(), makeReq(map[string]any{
		"ids": []any{sym.ID}, "project": "skel", "detail": "skeleton",
		"fields": "id,name,source",
	}))
	if err != nil {
		t.Fatalf("handleSymbols: %v", err)
	}
	body := decode(t, res)
	meta, _ := body["_meta"].(map[string]any)
	if meta == nil || meta["skeleton"] != true {
		t.Errorf("batch response missing top-level _meta.skeleton=true; _meta=%v", body["_meta"])
	}
	entries, _ := body["symbols"].([]any)
	if len(entries) != 1 {
		t.Fatalf("want 1 entry, got %d", len(entries))
	}
	entry, _ := entries[0].(map[string]any)
	src, _ := entry["source"].(string)
	if src == skelFixtureSrc || !strings.Contains(src, "… ") {
		t.Errorf("batch entry source not skeletonized:\n%s", src)
	}
	if db.ApproxTokens(src) >= db.ApproxTokens(skelFixtureSrc) {
		t.Errorf("batch skeleton not smaller than full source")
	}
	for _, callee := range skelFixtureCallees {
		if !containsIdent(src, callee) {
			t.Errorf("batch skeleton missing CALLS-edge callee %q", callee)
		}
	}
	// Per-entry markers would be redundant weight; assert we didn't add them.
	if entryMeta, _ := entry["_meta"].(map[string]any); entryMeta != nil {
		if _, present := entryMeta["skeleton"]; present {
			t.Errorf("per-entry _meta.skeleton found — the marker is top-level only")
		}
	}
}

// context detail=skeleton: primary symbol source is skeletonized and the
// #655 diff cache is bypassed — skeleton calls neither read nor populate
// it, so full and skeleton representations can't poison each other.
func TestContext_DetailSkeleton_BypassesDiffCache(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	srv.diffContext = true
	sym := seedSkeletonFixture(t, srv)
	srv.sessionID = "skel"

	// Two skeleton calls: both must return the skeleton — never
	// {unchanged:true}, never a diff (cache untouched).
	for i := 0; i < 2; i++ {
		res, err := srv.handleContext(context.Background(), makeReq(map[string]any{
			"id": sym.ID, "detail": "skeleton",
		}))
		if err != nil {
			t.Fatalf("skeleton call %d: %v", i, err)
		}
		body := decode(t, res)
		if body["unchanged"] != nil {
			t.Fatalf("skeleton call %d hit the diff cache: unchanged=%v", i, body["unchanged"])
		}
		sm, _ := body["symbol"].(map[string]any)
		if sm == nil {
			t.Fatalf("skeleton call %d missing symbol map: %v", i, body)
		}
		if sm["diff"] != nil {
			t.Fatalf("skeleton call %d returned a diff — representations crossed", i)
		}
		src, _ := sm["source"].(string)
		if !strings.Contains(src, "… ") {
			t.Errorf("skeleton call %d: source not skeletonized:\n%s", i, src)
		}
		meta, _ := body["_meta"].(map[string]any)
		if meta == nil || meta["skeleton"] != true {
			t.Errorf("skeleton call %d missing _meta.skeleton", i)
		}
	}

	// First FULL call after the skeleton calls: must be a clean cache
	// miss → verbatim full source (a poisoned cache would short-circuit
	// to unchanged or diff against a skeleton body).
	res, err := srv.handleContext(context.Background(), makeReq(map[string]any{"id": sym.ID}))
	if err != nil {
		t.Fatalf("full call: %v", err)
	}
	body := decode(t, res)
	if body["unchanged"] != nil {
		t.Fatalf("first full call short-circuited to unchanged — diff cache was poisoned by skeleton calls")
	}
	sm, _ := body["symbol"].(map[string]any)
	if src, _ := sm["source"].(string); src != skelFixtureSrc {
		t.Errorf("first full call must return verbatim source (cache miss); got %d bytes want %d", len(src), len(skelFixtureSrc))
	}

	// Second full call: normal #655 behaviour resumes (unchanged file →
	// short-circuit). Proves the bypass is skeleton-scoped, not a global
	// disable.
	res, err = srv.handleContext(context.Background(), makeReq(map[string]any{"id": sym.ID}))
	if err != nil {
		t.Fatalf("second full call: %v", err)
	}
	body = decode(t, res)
	if body["unchanged"] != true {
		t.Errorf("second full call should short-circuit on the diff cache; got %v", body)
	}
}

// context detail=skeleton compresses dependency sources too.
func TestContext_DetailSkeleton_CompressesCallees(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	sym := seedSkeletonFixture(t, srv)
	srv.sessionID = "skel"

	// Give one callee a real on-disk body so its source is non-empty.
	calleeID := "skel::orders.parseInput#Function"
	root, err := srv.resolveProjectRoot("skel")
	if err != nil {
		t.Fatalf("resolveProjectRoot: %v", err)
	}
	calleeSrc := "func parseInput(raw []byte) (*Order, error) {\n\tvar o Order\n\tx := 1\n\ty := 2\n\tz := 3\n\t_ = x + y + z\n\tif err := json.Unmarshal(raw, &o); err != nil {\n\t\treturn nil, err\n\t}\n\treturn &o, nil\n}\n"
	if err := os.WriteFile(filepath.Join(root, "helpers.go"), []byte(calleeSrc), 0o600); err != nil {
		t.Fatalf("write callee file: %v", err)
	}
	if err := srv.store.BulkUpsertSymbols([]db.Symbol{{
		ID: calleeID, ProjectID: "skel", FilePath: "helpers.go",
		Name: "parseInput", QualifiedName: "orders.parseInput",
		Kind: "Function", Language: "Go",
		StartByte: 0, EndByte: len(calleeSrc),
		StartLine: 1, EndLine: strings.Count(calleeSrc, "\n") + 1,
		ExtractionConfidence: 1.0,
	}}); err != nil {
		t.Fatalf("upsert callee: %v", err)
	}

	res, err := srv.handleContext(context.Background(), makeReq(map[string]any{
		"id": sym.ID, "detail": "skeleton",
	}))
	if err != nil {
		t.Fatalf("handleContext: %v", err)
	}
	body := decode(t, res)
	callees, _ := body["callees"].([]any)
	var got string
	for _, c := range callees {
		cm, _ := c.(map[string]any)
		if cm["name"] == "parseInput" {
			got, _ = cm["source"].(string)
		}
	}
	if got == "" {
		t.Fatalf("parseInput callee source missing: %v", body["callees"])
	}
	if got == calleeSrc || !strings.Contains(got, "… ") {
		t.Errorf("callee source not skeletonized:\n%s", got)
	}
}

// Pure-function shape checks on the line classifier.
func TestSkeletonize_FlowLinesKeptVerbatim(t *testing.T) {
	t.Parallel()
	skel := skeletonize(skelFixtureSrc, skelFixtureCallees)
	for _, want := range []string{
		"\tif err != nil {",
		"\tfor _, line := range order.Lines {",
		"\tswitch order.Priority {",
		"\tcase PriorityRush:",
		"\tdefault:",
		"\tselect {",
		"\tdefer trackDuration(startedAt)",
		"\treturn &receipt, nil",
	} {
		if !strings.Contains(skel, want+"\n") && !strings.HasSuffix(skel, want) {
			t.Errorf("flow line %q missing from skeleton:\n%s", want, skel)
		}
	}
	// Straight-line filler must be gone.
	for _, gone := range []string{"startedAt := time.Now()", "receipt.Tax = totals.Tax"} {
		if strings.Contains(skel, gone) {
			t.Errorf("straight-line statement %q should be elided", gone)
		}
	}
}

// Tiny sources pass through untouched — nothing to elide.
func TestSkeletonize_TinySourcePassthrough(t *testing.T) {
	t.Parallel()
	src := "func one() int { return 1 }"
	if got := skeletonize(src, nil); got != src {
		t.Errorf("tiny source must pass through; got %q", got)
	}
}
