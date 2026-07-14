package server

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

func TestHandleContext_TokenSavingDefersDependencyBodies(t *testing.T) {
	srv, store, root := newTestServer(t)
	projectID := "ctx-save"
	store.UpsertProject(db.Project{ID: projectID, Path: root, Name: projectID, IndexedAt: time.Now()})
	srv.sessionID = projectID
	primaryID := "main.go::main#Function"
	depID := "helper.go::helper#Function"
	primarySource := "func main() { helper() }\n"
	depSource := "func helper() { return }\n"
	if err := os.WriteFile(root+"/main.go", []byte(primarySource), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(root+"/helper.go", []byte(depSource), 0o644); err != nil {
		t.Fatal(err)
	}
	store.BulkUpsertSymbols([]db.Symbol{
		{ID: primaryID, ProjectID: projectID, Name: "main", QualifiedName: "main", Kind: "Function", Language: "Go", FilePath: "main.go", StartByte: 0, EndByte: len(primarySource), StartLine: 1, EndLine: 1},
		{ID: depID, ProjectID: projectID, Name: "helper", QualifiedName: "helper", Kind: "Function", Language: "Go", FilePath: "helper.go", StartByte: 0, EndByte: len(depSource), StartLine: 1, EndLine: 1},
	})
	if err := store.BulkUpsertEdges([]db.Edge{{ProjectID: projectID, FromID: primaryID, ToID: depID, Kind: "CALLS", Confidence: 1}}); err != nil {
		t.Fatal(err)
	}

	res, err := srv.handleContext(context.Background(), makeReq(map[string]any{"id": primaryID, "token_mode": "save"}))
	if err != nil {
		t.Fatal(err)
	}
	body := decode(t, res)
	callees, _ := body["callees"].([]any)
	if len(callees) != 1 {
		t.Fatalf("callees = %#v", body["callees"])
	}
	callee := callees[0].(map[string]any)
	if _, ok := callee["source"]; ok {
		t.Fatalf("save mode returned dependency source: %#v", callee)
	}
	if omitted, _ := callee["source_omitted"].(bool); !omitted {
		t.Fatalf("dependency missing source_omitted marker: %#v", callee)
	}
	saveTokens := db.ApproxTokens(textOf(t, res))

	res, err = srv.handleContext(context.Background(), makeReq(map[string]any{"id": primaryID, "token_mode": "save", "detail": "full"}))
	if err != nil {
		t.Fatal(err)
	}
	body = decode(t, res)
	callees, _ = body["callees"].([]any)
	if _, ok := callees[0].(map[string]any)["source"]; !ok {
		t.Fatalf("explicit detail=full did not restore dependency source: %#v", callees[0])
	}
	fullTokens := db.ApproxTokens(textOf(t, res))
	t.Logf("context payload tokens: save=%d full=%d saved=%d (%.1f%%)", saveTokens, fullTokens, fullTokens-saveTokens, float64(fullTokens-saveTokens)/float64(fullTokens)*100)
}
