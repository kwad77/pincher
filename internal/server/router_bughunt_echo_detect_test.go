// SPDX-License-Identifier: MIT

package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"
)

// Bug-hunt #2036 follow-up: four confirmed defects in the route echo
// cache + router-identity detection. Each test fails on the pre-fix code
// and passes after the fix:
//
//   MED-1  saveLocked used a FIXED temp name (persistPath+".tmp"), so two
//          processes sharing one data-dir sidecar raced the same temp →
//          torn JSON on load. The unique-temp fix gives each writer its
//          own temp; only the atomic rename competes.
//   MED-2  echo_source claimed "caller" (and suppressed the echo_miss
//          warning) whenever the caller supplied session_id alone, even
//          on a cache miss with a card the router will 422. "caller" now
//          requires a COMPLETE card or a cache hit.
//   LOW-3  RouterHealthzIdentity accepted {"weights_version": null} (and
//          any arbitrary value) on key presence alone, yielding wv="<nil>".
//          A null/empty handshake value is now rejected.
//   LOW-4  loadLocked appended every Order id without dedup, so a sidecar
//          with a duplicated id made len(order) > len(entries) and the
//          cap loop evicted a live entry. A `seen` set keeps order a true
//          permutation of entries.

// --- MED-1: concurrent multi-instance writes never tear the sidecar ----

// TestBughunt_SaveLocked_ConcurrentWritersNoTornJSON is the MED-1 repro.
// Many goroutines, each with its OWN routeEchoCache pointed at the SAME
// persistPath (the in-test analogue of multiple processes sharing one
// data-dir sidecar), hammer saveLocked while a reader continuously loads.
// With the fixed temp name, racing renames + the loser's os.Remove tore
// the file (the finder saw ~17/2000 invalid loads). With a unique temp
// per write, every load that sees a file sees VALID JSON.
func TestBughunt_SaveLocked_ConcurrentWritersNoTornJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, routeEchoPersistFile)

	const writers = 8
	const iters = 300

	var writeWG sync.WaitGroup
	var readWG sync.WaitGroup
	stop := make(chan struct{})

	// Writers: each owns a distinct cache (distinct process) but the
	// same sidecar path.
	for w := 0; w < writers; w++ {
		writeWG.Add(1)
		go func(w int) {
			defer writeWG.Done()
			c := &routeEchoCache{persistPath: path}
			for i := 0; i < iters; i++ {
				c.mu.Lock()
				c.entries = map[string]map[string]any{
					"id-" + strconv.Itoa(w): {
						"session_id":   "sess-" + strconv.Itoa(w),
						"routed_model": "m-" + strconv.Itoa(i),
						"lane":         "fast",
					},
				}
				c.order = []string{"id-" + strconv.Itoa(w)}
				c.saveLocked()
				c.mu.Unlock()
			}
		}(w)
	}

	// Reader: continuously parse whatever is at path. A torn write makes
	// json.Unmarshal fail — that is the bug.
	var tornReads int64
	var readMu sync.Mutex
	readWG.Add(1)
	go func() {
		defer readWG.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			raw, err := os.ReadFile(path)
			if err != nil || len(raw) == 0 {
				continue // file mid-rename / not yet created — not a tear
			}
			var snap routeEchoSnapshot
			if err := json.Unmarshal(raw, &snap); err != nil {
				readMu.Lock()
				tornReads++
				readMu.Unlock()
			}
		}
	}()

	// Wait for the writers to drain, then signal the reader to stop.
	writeWG.Wait()
	close(stop)
	readWG.Wait()

	if tornReads != 0 {
		t.Fatalf("MED-1: %d torn (invalid-JSON) sidecar reads under concurrent writers — unique-temp-per-write must eliminate them", tornReads)
	}

	// A leak check: no stray .tmp files should survive a clean run.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Errorf("MED-1: leftover temp file %q after writes settled", e.Name())
		}
	}

	// Sanity: the final sidecar is loadable.
	final := &routeEchoCache{persistPath: path}
	final.mu.Lock()
	final.loadLocked()
	final.mu.Unlock()
}

// --- MED-2: partial caller echo is "none", not a lying "caller" --------

// TestBughunt_EchoSource_PartialCallerEchoIsNotCaller is the MED-2 repro.
// Cache miss, and the caller supplied ONLY session_id — none of the other
// required keys (tool_name, complexity_tier, role, routed_model, lane).
// The router will 422 this body. Pre-fix: echo_source="caller" and the
// echo_miss warning was suppressed, hiding a real failure. Post-fix:
// echo_source must NOT be "caller" (it is "none") so the miss is visible.
func TestBughunt_EchoSource_PartialCallerEchoIsNotCaller(t *testing.T) {
	fake := defaultFakeRouter(t)
	srv := newRouterToolServer(t, fake)

	res := callRoute(t, srv, map[string]any{
		"action": "outcome",
		"outcome": map[string]any{
			"request_id":    "feedfacefeedfacefeedfacefeedface", // never routed
			"outcome_class": "clean",
			"gate":          "S5",
			"session_id":    "partial-caller-echo", // only this echo key
		},
	})
	if res.IsError {
		t.Fatalf("outcome errored: %s", textOf(t, res))
	}
	body := decode(t, res)
	if body["echo_source"] == "caller" {
		t.Fatalf("MED-2: partial caller echo (session_id only, cache miss) claimed echo_source=\"caller\" — a body the router will 422 must not look like the happy path; want \"none\"")
	}
	if body["echo_source"] != "none" {
		t.Errorf("MED-2: partial caller echo echo_source = %v, want \"none\"", body["echo_source"])
	}
}

// TestBughunt_EchoSource_CompleteCallerEchoIsCaller pins the other side:
// a cache miss where the caller DID supply every required key is a body
// the router accepts, so echo_source="caller" is honest.
func TestBughunt_EchoSource_CompleteCallerEchoIsCaller(t *testing.T) {
	fake := defaultFakeRouter(t)
	srv := newRouterToolServer(t, fake)

	res := callRoute(t, srv, map[string]any{
		"action": "outcome",
		"outcome": map[string]any{
			"request_id":      "feedfacefeedfacefeedfacefeedface",
			"outcome_class":   "clean",
			"gate":            "S5",
			"session_id":      "complete-caller-echo",
			"tool_name":       "Task",
			"complexity_tier": "lite",
			"role":            "fix",
			"routed_model":    "qwen3.6-35b-a3b",
			"lane":            "fast",
		},
	})
	if res.IsError {
		t.Fatalf("outcome errored: %s", textOf(t, res))
	}
	if body := decode(t, res); body["echo_source"] != "caller" {
		t.Errorf("MED-2: complete caller echo on cache miss echo_source = %v, want \"caller\"", body["echo_source"])
	}
}

// --- LOW-3: identity gate rejects null/empty weights_version -----------

// TestBughunt_Identity_NullWeightsVersionRejected is the LOW-3 repro. A
// 200 carrying {"weights_version": null} is NOT a pincher-router; pre-fix
// it passed (presence-only) and identified as router with wv="<nil>".
func TestBughunt_Identity_NullWeightsVersionRejected(t *testing.T) {
	srv, _ := fakeRouter(t, 200, `{"ok":true,"weights_version":null}`)
	wv, ok := RouterHealthzIdentity(srv.URL, time.Second)
	if ok {
		t.Fatalf("LOW-3: {\"weights_version\": null} identified as router (wv=%q) — a null handshake value must be rejected", wv)
	}
}

// TestBughunt_Identity_EmptyStringWeightsVersionRejected: "" is likewise
// not a valid handshake value.
func TestBughunt_Identity_EmptyStringWeightsVersionRejected(t *testing.T) {
	srv, _ := fakeRouter(t, 200, `{"weights_version":""}`)
	if wv, ok := RouterHealthzIdentity(srv.URL, time.Second); ok {
		t.Fatalf("LOW-3: empty-string weights_version identified as router (wv=%q) — must be rejected", wv)
	}
}

// TestBughunt_Identity_RealWeightsVersionStillAccepted guards against
// over-tightening: a genuine non-empty value (string OR number) is still
// accepted and returned verbatim.
func TestBughunt_Identity_RealWeightsVersionStillAccepted(t *testing.T) {
	t.Run("string", func(t *testing.T) {
		srv, _ := fakeRouter(t, 200, `{"weights_version":"2026-06-12T00:00:00Z"}`)
		wv, ok := RouterHealthzIdentity(srv.URL, time.Second)
		if !ok {
			t.Fatal("LOW-3: real string weights_version rejected — must still be accepted")
		}
		if wv != "2026-06-12T00:00:00Z" {
			t.Errorf("weights_version = %q, want the verbatim value", wv)
		}
	})
	t.Run("number", func(t *testing.T) {
		srv, _ := fakeRouter(t, 200, `{"weights_version":42}`)
		wv, ok := RouterHealthzIdentity(srv.URL, time.Second)
		if !ok {
			t.Fatal("LOW-3: numeric weights_version rejected — must still be accepted")
		}
		if wv != "42" {
			t.Errorf("weights_version = %q, want \"42\"", wv)
		}
	})
}

// --- LOW-4: loadLocked dedups Order so it never early-evicts -----------

// TestBughunt_LoadLocked_DuplicateOrderNoEarlyEvict is the LOW-4 repro. A
// sidecar whose Order slice contains a duplicate id must load with
// len(order) == len(entries); pre-fix the dup was appended twice, making
// order longer than entries so the cap loop evicted a LIVE entry.
func TestBughunt_LoadLocked_DuplicateOrderNoEarlyEvict(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, routeEchoPersistFile)

	// Build a sidecar with cap distinct entries but a DUPLICATED id in
	// Order — id "dup" appears twice. Total Order length = cap+1.
	entries := make(map[string]map[string]any, routeEchoCacheCap)
	order := make([]string, 0, routeEchoCacheCap+1)
	order = append(order, "dup") // the duplicate's first appearance
	entries["dup"] = map[string]any{"session_id": "s-dup", "routed_model": "m-dup", "lane": "fast"}
	for i := 0; i < routeEchoCacheCap-1; i++ {
		id := "id-" + strconv.Itoa(i)
		entries[id] = map[string]any{"session_id": "s-" + strconv.Itoa(i), "routed_model": "m-" + strconv.Itoa(i), "lane": "fast"}
		order = append(order, id)
	}
	order = append(order, "dup") // duplicate again at the end

	raw, err := json.Marshal(routeEchoSnapshot{Order: order, Entries: entries})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	c := &routeEchoCache{persistPath: path}
	c.mu.Lock()
	c.loadLocked()
	gotOrder := len(c.order)
	gotEntries := len(c.entries)
	_, dupAlive := c.entries["dup"]
	_, firstAlive := c.entries["id-0"]
	c.mu.Unlock()

	if gotOrder != gotEntries {
		t.Fatalf("LOW-4: len(order)=%d != len(entries)=%d after a duplicated Order id — order must be a true permutation of entries", gotOrder, gotEntries)
	}
	if !dupAlive {
		t.Errorf("LOW-4: the entry whose id was duplicated in Order is missing after load")
	}
	if !firstAlive {
		t.Errorf("LOW-4: a live entry (id-0) was early-evicted by the phantom duplicate slot")
	}
	if gotEntries != routeEchoCacheCap {
		t.Errorf("LOW-4: loaded %d entries, want all %d to survive (no phantom-slot eviction)", gotEntries, routeEchoCacheCap)
	}
}
