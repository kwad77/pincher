package index

import (
	"context"
	"testing"

	"github.com/kwad77/pincher/internal/db"
)

func countEdgesByKind(t *testing.T, idx *Indexer, projectID, kind string) int {
	t.Helper()
	var n int
	if err := idx.store.DB().QueryRow(
		`SELECT COUNT(*) FROM edges WHERE project_id=? AND kind=?`,
		projectID, kind,
	).Scan(&n); err != nil {
		t.Fatalf("count %s edges: %v", kind, err)
	}
	return n
}

func fileModuleID(t *testing.T, store *db.Store, projectID, filePath string) string {
	t.Helper()
	syms, err := store.GetSymbolsForFile(projectID, filePath)
	if err != nil {
		t.Fatalf("GetSymbolsForFile(%s): %v", filePath, err)
	}
	for _, s := range syms {
		if s.Kind == "Module" {
			return s.ID
		}
	}
	t.Fatalf("no Module symbol for %s; syms=%+v", filePath, syms)
	return ""
}

func TestIndex_AnsibleStructuralEdges_PersistedToDB_1869(t *testing.T) {
	idx, store := newTestIndexer(t)
	dir := t.TempDir()

	writeFile(t, dir, "site.yml", `---
- import_playbook: playbooks/db.yml
- hosts: web
  roles:
    - web
  tasks:
    - import_role: { name: common }
    - include_tasks: tasks/bootstrap.yml
`)
	writeFile(t, dir, "roles/web/tasks/main.yml", `---
- debug: { msg: web }
`)
	writeFile(t, dir, "roles/common/tasks/main.yml", `---
- debug: { msg: common }
`)
	writeFile(t, dir, "tasks/bootstrap.yml", `---
- debug: { msg: bootstrap }
`)
	writeFile(t, dir, "playbooks/db.yml", `---
- hosts: db
  tasks:
    - debug: { msg: db }
`)
	writeFile(t, dir, "inventory/hosts.yml", `---
all:
  hosts:
    web-01:
      ansible_host: 10.0.1.1
`)
	writeFile(t, dir, "host_vars/web-01.yml", "ansible_user: deploy\n")

	res, err := idx.Index(context.Background(), dir, false)
	if err != nil {
		t.Fatalf("Index: %v", err)
	}
	projectID := res.ProjectID

	if got := countEdgesByKind(t, idx, projectID, "INCLUDES"); got < 4 {
		t.Fatalf("INCLUDES persisted count = %d, want at least 4", got)
	}
	if got := countEdgesByKind(t, idx, projectID, "LOADS"); got < 1 {
		t.Fatalf("LOADS persisted count = %d, want at least 1", got)
	}

	roleTarget := fileModuleID(t, store, projectID, "roles/web/tasks/main.yml")
	includeEdges, err := store.EdgesTo(roleTarget, []string{"INCLUDES"})
	if err != nil {
		t.Fatalf("EdgesTo INCLUDES: %v", err)
	}
	if len(includeEdges) == 0 {
		t.Fatal("expected playbook role edge to persist as INCLUDES; got 0")
	}

	hostVarsTarget := fileModuleID(t, store, projectID, "host_vars/web-01.yml")
	loadEdges, err := store.EdgesTo(hostVarsTarget, []string{"LOADS"})
	if err != nil {
		t.Fatalf("EdgesTo LOADS: %v", err)
	}
	if len(loadEdges) == 0 {
		t.Fatal("expected inventory host_vars edge to persist as LOADS; got 0")
	}
}
