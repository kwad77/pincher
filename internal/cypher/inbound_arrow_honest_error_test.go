// SPDX-License-Identifier: MIT

package cypher

import (
	"context"
	"strings"
	"testing"
)

// #1115: the inbound arrow form `<-[r:KIND]-` was suggested as a
// remediation in the undirected-edge error message, but it was never
// actually implemented — the parser rejected `<-[r:KIND]-` with the
// same "undirected" error, leading agents into a loop. Fix replaces
// the misleading remediation with a variable-swap workaround using
// the supported outbound arrow form.

func TestExecute_InboundArrow_RejectedWithHonestRemediation(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertSym(t, db, "a", "Open", "Function", "Go")
	insertSym(t, db, "b", "Caller", "Function", "Go")

	e := &Executor{DB: db, MaxRows: 100, ProjectID: "proj1"}
	_, err := e.Execute(context.Background(),
		`MATCH (a)<-[r:CALLS]-(b) WHERE a.name = "Open" RETURN b.name`)
	if err == nil {
		t.Fatal("inbound arrow `<-[r:KIND]-` must be rejected (not implemented)")
	}
	msg := err.Error()
	// Must point at the actual workaround.
	if !strings.Contains(msg, "swap the variables") {
		t.Errorf("error message must name the variable-swap workaround; got %q", msg)
	}
	if !strings.Contains(msg, "outbound arrow") || !strings.Contains(msg, "-[r:KIND]->") {
		t.Errorf("error message must reference the supported outbound form; got %q", msg)
	}
	// Pre-fix the remediation was: "Use -[r:KIND]-> for outbound,
	// <-[r:KIND]- for inbound" — the inbound form was suggested as the
	// remediation for itself. Post-fix the message uses <-[r:KIND]-
	// ONLY as a negative example ("instead of ..."), not as a directly-
	// recommended form. Guard against regression by asserting the
	// "for inbound" recommendation no longer fires.
	if strings.Contains(msg, "for inbound") {
		t.Errorf("error message must not recommend an 'inbound' form (the input it just rejected); got %q", msg)
	}
}
