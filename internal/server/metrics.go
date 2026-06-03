// SPDX-License-Identifier: MIT

package server

import (
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// osFileInfo is the subset of os.FileInfo we actually consume —
// just Size(). Keeping the interface narrow makes the test-time
// substitution unambiguous.
type osFileInfo interface {
	Size() int64
}

// osStat is the indirection seam. Default is os.Stat; tests can
// override.
var osStat = func(name string) (osFileInfo, error) {
	info, err := os.Stat(name)
	if err != nil {
		return nil, err
	}
	return info, nil
}

// #1163: Prometheus metrics — hand-rolled in-process registry.
//
// Why not prometheus/client_golang: the official library is the gold
// standard but pulls ~10 transitive deps (descriptors, registry,
// collectors, gobwas escape, hashmap). Pincher's ethos is "one Go
// binary, minimal deps"; for a counter + gauge + simple latency
// aggregation surface we don't need the full HDR-histogram + label-
// cardinality protection that the official lib provides. If pincher
// ever needs label-cardinality limits or true Prometheus histograms
// with buckets, that's the cue to adopt the official lib.
//
// What this ships (v0.67):
//   - Counters with optional label set (single label-pair max for
//     simplicity — `pincher_tool_calls_total{tool="search"}` etc.)
//   - Gauges — single-value snapshots updated synchronously
//   - Latency summary — count + sum per label, yielding the average
//     latency when scraped. Bucketed histograms are a v0.68 follow-up
//     once we see Prometheus query patterns from real usage.
//   - /v1/metrics endpoint rendering Prometheus 0.0.4 exposition format
//
// Concurrency: atomic primitives for counters and gauges; sync.Mutex
// for the labelled-counter map mutations. Read paths (exposition) take
// the same lock briefly. Hot-path tool-call instrumentation hits the
// labelled counter once per call — sub-microsecond overhead.

const (
	// Metric names. Stable contract — Prometheus dashboards bind to
	// these strings. Treat changes here like API changes.
	metricToolCallsTotal     = "pincher_tool_calls_total"
	metricToolLatencySeconds = "pincher_tool_latency_seconds"
	metricToolTokensSaved    = "pincher_tool_tokens_saved_total"
	metricIndexFilesTotal    = "pincher_index_files_total"
	metricIndexSymbolsTotal  = "pincher_index_symbols_total"
	metricDBSizeBytes        = "pincher_db_size_bytes"
	metricWALSizeBytes       = "pincher_wal_size_bytes"
)

// metricsRegistry is the in-process collector. All operations are
// concurrency-safe.
type metricsRegistry struct {
	mu sync.Mutex

	// counters: name → label-value → uint64 monotonic count.
	// The "label-value" key is "" for unlabelled counters or e.g.
	// "tool=search" for labelled. We store the full label expression
	// as the key to keep the data model simple — exposition splits
	// it back into the {label="value"} form.
	counters map[string]map[string]*atomic.Uint64

	// summaries: name → label-value → (count, sum-of-observations).
	// Sum is stored as the bit-pattern of a float64 so we can use
	// atomic CAS for lock-free updates on the hot path.
	summaries map[string]map[string]*summaryEntry

	// gauges: name → atomic float64 bit-pattern. Unlabelled today
	// (only db_size + wal_size); labelled gauges land if/when we need
	// them. Stored as bits so atomic.LoadUint64 works.
	gauges map[string]*atomic.Uint64
}

type summaryEntry struct {
	count   atomic.Uint64
	sumBits atomic.Uint64 // bits of accumulated sum (float64)
}

func newMetricsRegistry() *metricsRegistry {
	return &metricsRegistry{
		counters:  make(map[string]map[string]*atomic.Uint64),
		summaries: make(map[string]map[string]*summaryEntry),
		gauges:    make(map[string]*atomic.Uint64),
	}
}

// IncCounter bumps the named counter by 1 under the given label
// expression. Pass labels as a flat sequence: IncCounter(name, 1,
// "tool", "search", "outcome", "ok"). Empty key/value pairs are
// dropped silently — keeps the call-site clean when the caller only
// has a partial set.
func (m *metricsRegistry) IncCounter(name string, n uint64, labelPairs ...string) {
	key := labelKey(labelPairs)
	m.mu.Lock()
	bucket, ok := m.counters[name]
	if !ok {
		bucket = map[string]*atomic.Uint64{}
		m.counters[name] = bucket
	}
	c, ok := bucket[key]
	if !ok {
		c = &atomic.Uint64{}
		bucket[key] = c
	}
	m.mu.Unlock()
	c.Add(n)
}

// ObserveSummary records one observation (e.g. latency seconds) into
// the summary's count + sum. Average = sum / count at scrape time.
func (m *metricsRegistry) ObserveSummary(name string, value float64, labelPairs ...string) {
	key := labelKey(labelPairs)
	m.mu.Lock()
	bucket, ok := m.summaries[name]
	if !ok {
		bucket = map[string]*summaryEntry{}
		m.summaries[name] = bucket
	}
	entry, ok := bucket[key]
	if !ok {
		entry = &summaryEntry{}
		bucket[key] = entry
	}
	m.mu.Unlock()
	entry.count.Add(1)
	// Lock-free float64 add via CAS loop on the bit pattern.
	for {
		oldBits := entry.sumBits.Load()
		newBits := math.Float64bits(math.Float64frombits(oldBits) + value)
		if entry.sumBits.CompareAndSwap(oldBits, newBits) {
			return
		}
	}
}

// SetGauge writes the gauge's current value. Concurrent writers race
// — the last write wins, which matches Prometheus gauge semantics
// (it's a current-value snapshot, not a derivative).
func (m *metricsRegistry) SetGauge(name string, value float64) {
	m.mu.Lock()
	g, ok := m.gauges[name]
	if !ok {
		g = &atomic.Uint64{}
		m.gauges[name] = g
	}
	m.mu.Unlock()
	g.Store(math.Float64bits(value))
}

// Exposition renders all currently-registered metrics in the
// Prometheus 0.0.4 text exposition format. Stable iteration order
// (alphabetical by metric name, then by label key) keeps the output
// diffable in tests.
func (m *metricsRegistry) Exposition() string {
	m.mu.Lock()
	// Snapshot the metric-name keys; we'll release the lock during
	// the per-counter atomic reads to keep critical-section time short.
	counterNames := make([]string, 0, len(m.counters))
	for n := range m.counters {
		counterNames = append(counterNames, n)
	}
	summaryNames := make([]string, 0, len(m.summaries))
	for n := range m.summaries {
		summaryNames = append(summaryNames, n)
	}
	gaugeNames := make([]string, 0, len(m.gauges))
	for n := range m.gauges {
		gaugeNames = append(gaugeNames, n)
	}
	// Local snapshots of the bucket maps (pointers — the atomics inside
	// are still concurrency-safe). This lets us iterate without
	// re-acquiring the registry lock per metric.
	counterSnap := map[string]map[string]*atomic.Uint64{}
	for _, n := range counterNames {
		counterSnap[n] = m.counters[n]
	}
	summarySnap := map[string]map[string]*summaryEntry{}
	for _, n := range summaryNames {
		summarySnap[n] = m.summaries[n]
	}
	gaugeSnap := map[string]*atomic.Uint64{}
	for _, n := range gaugeNames {
		gaugeSnap[n] = m.gauges[n]
	}
	m.mu.Unlock()

	sort.Strings(counterNames)
	sort.Strings(summaryNames)
	sort.Strings(gaugeNames)

	var b strings.Builder
	for _, name := range counterNames {
		fmt.Fprintf(&b, "# HELP %s Cumulative counter.\n", name)
		fmt.Fprintf(&b, "# TYPE %s counter\n", name)
		bucket := counterSnap[name]
		keys := sortedKeys(bucket)
		for _, k := range keys {
			fmt.Fprintf(&b, "%s%s %d\n", name, formatLabelKeyForExposition(k), bucket[k].Load())
		}
	}
	for _, name := range summaryNames {
		fmt.Fprintf(&b, "# HELP %s Summary (count + sum; average = sum/count).\n", name)
		fmt.Fprintf(&b, "# TYPE %s summary\n", name)
		bucket := summarySnap[name]
		keys := sortedKeys(bucket)
		for _, k := range keys {
			entry := bucket[k]
			count := entry.count.Load()
			sum := math.Float64frombits(entry.sumBits.Load())
			fmt.Fprintf(&b, "%s_count%s %d\n", name, formatLabelKeyForExposition(k), count)
			fmt.Fprintf(&b, "%s_sum%s %s\n", name, formatLabelKeyForExposition(k), formatFloat(sum))
		}
	}
	for _, name := range gaugeNames {
		fmt.Fprintf(&b, "# HELP %s Gauge (current snapshot).\n", name)
		fmt.Fprintf(&b, "# TYPE %s gauge\n", name)
		fmt.Fprintf(&b, "%s %s\n", name, formatFloat(math.Float64frombits(gaugeSnap[name].Load())))
	}
	return b.String()
}

// labelKey collapses a flat key/value sequence into a stable string
// like "outcome=ok,tool=search". Empty entries are dropped; pairs
// with empty value or key are skipped (defensive — keeps the registry
// from accumulating noise from partially-filled call sites).
func labelKey(pairs []string) string {
	if len(pairs) == 0 {
		return ""
	}
	type kv struct{ k, v string }
	out := make([]kv, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		if pairs[i] == "" || pairs[i+1] == "" {
			continue
		}
		out = append(out, kv{pairs[i], pairs[i+1]})
	}
	if len(out) == 0 {
		return ""
	}
	sort.Slice(out, func(i, j int) bool { return out[i].k < out[j].k })
	parts := make([]string, len(out))
	for i, p := range out {
		parts[i] = p.k + "=" + p.v
	}
	return strings.Join(parts, ",")
}

// formatLabelKeyForExposition converts the internal labelKey form
// ("outcome=ok,tool=search") into the Prometheus exposition form
// ('{outcome="ok",tool="search"}'). Returns empty string for the
// unlabelled case so the line reads `name 42` not `name{} 42`.
func formatLabelKeyForExposition(key string) string {
	if key == "" {
		return ""
	}
	pairs := strings.Split(key, ",")
	parts := make([]string, len(pairs))
	for i, p := range pairs {
		eq := strings.IndexByte(p, '=')
		if eq < 0 {
			parts[i] = p
			continue
		}
		k := p[:eq]
		v := p[eq+1:]
		parts[i] = k + `="` + escapePromLabelValue(v) + `"`
	}
	return "{" + strings.Join(parts, ",") + "}"
}

// escapePromLabelValue escapes backslash, double-quote, and newline
// per the Prometheus exposition spec.
func escapePromLabelValue(v string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)
	return r.Replace(v)
}

// formatFloat renders a float64 as the Prometheus exposition expects
// — integers as `42`, fractions as `0.001234`, no scientific notation
// unless the magnitude makes it unavoidable.
func formatFloat(v float64) string {
	if math.IsNaN(v) {
		return "NaN"
	}
	if math.IsInf(v, 1) {
		return "+Inf"
	}
	if math.IsInf(v, -1) {
		return "-Inf"
	}
	// Integers stay integers in the rendering — Prometheus accepts both.
	if v == math.Trunc(v) && math.Abs(v) < 1e15 {
		return fmt.Sprintf("%d", int64(v))
	}
	// Otherwise use %g with enough precision to round-trip the value.
	return fmt.Sprintf("%g", v)
}

// refreshDBGauges updates the db_size_bytes + wal_size_bytes gauges
// from the on-disk filesystem state. Called synchronously at /v1/metrics
// scrape time — two os.Stat calls is well under any reasonable
// Prometheus scrape budget (~1ms even on slow filesystems). Avoids
// a background ticker + goroutine lifecycle for what is intrinsically
// scrape-time information.
func (s *Server) refreshDBGauges() {
	if s.metrics == nil || s.store == nil {
		return
	}
	dbPath := s.store.Path
	if info, err := osStatNoCache(dbPath); err == nil {
		s.metrics.SetGauge(metricDBSizeBytes, float64(info.Size()))
	}
	if info, err := osStatNoCache(dbPath + "-wal"); err == nil {
		s.metrics.SetGauge(metricWALSizeBytes, float64(info.Size()))
	}
}

// osStatNoCache wraps os.Stat. Indirection point for tests; current
// impl is the trivial pass-through. Named to make the no-cache
// expectation explicit at the call site — we want fresh size on
// every scrape, not a memoized snapshot.
func osStatNoCache(name string) (osFileInfo, error) {
	return osStat(name)
}

// sortedKeys returns the sorted key slice of a string-keyed map.
// Generic helper avoids type-switching at every call site.
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
