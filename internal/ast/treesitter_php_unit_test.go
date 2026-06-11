// SPDX-License-Identifier: MIT

package ast

import (
	"context"
	"testing"
)

func TestTreeSitterPHP_InlineExtract(t *testing.T) {
	const src = `<?php
namespace App\Models;

use App\Contracts\Repository;
use App\Support\Helper;

interface UserRepo {
    public function find(int $id): User;
}

enum Status {
    case Active;
    case Inactive;
}

class UserService implements UserRepo {
    public function __construct() {
        $this->init();
    }
    public function find(int $id): User {
        $h = new Helper();
        return $h->load($id);
    }
}

trait Loggable {
    public function log(string $msg): void {
        record($msg);
    }
}

function topLevel(): int {
    return 1;
}
`
	x, err := newPHPTSExtractor(context.Background())
	if err != nil {
		t.Fatalf("newPHPTSExtractor: %v", err)
	}
	fr, ok := x.extractChecked([]byte(src), "src/UserService.php")
	if !ok {
		t.Fatal("PHP parse hit an ERROR node on valid source")
	}

	byKind := map[string]map[string]bool{}
	qn := map[string]string{}
	parent := map[string]string{}
	for _, s := range fr.Symbols {
		if byKind[s.Kind] == nil {
			byKind[s.Kind] = map[string]bool{}
		}
		byKind[s.Kind][s.Name] = true
		qn[s.Name] = s.QualifiedName
		parent[s.Name] = s.Parent
		if s.ExtractionConfidence != 1.0 {
			t.Errorf("symbol %s: confidence %v, want 1.0", s.Name, s.ExtractionConfidence)
		}
	}
	for name, kind := range map[string]string{
		"UserService": "Class", "UserRepo": "Interface", "Status": "Enum", "topLevel": "Function",
	} {
		if !byKind[kind][name] {
			t.Errorf("missing %s %q; got %v", kind, name, byKind[kind])
		}
	}
	for _, m := range []string{"find", "__construct", "log"} {
		if !byKind["Method"][m] {
			t.Errorf("missing Method %q; got %v", m, byKind["Method"])
		}
	}
	// trait is scope-only: no Class symbol named Loggable, but its method
	// `log` parents to the trait's QN.
	if byKind["Class"]["Loggable"] {
		t.Error("trait Loggable should not emit a Class symbol (regex scopeRE parity)")
	}
	if parent["log"] != "src\\UserService\\Loggable" {
		t.Errorf("log parent = %q, want src\\UserService\\Loggable", parent["log"])
	}
	// namespace-blind QN: keyed on moduleQN(file), not the PHP `namespace`.
	if qn["find"] != "src\\UserService\\UserService\\find" {
		t.Errorf("find QN = %q, want src\\UserService\\UserService\\find", qn["find"])
	}

	imports := map[string]bool{}
	calls := map[string]bool{}
	for _, e := range fr.Edges {
		switch e.Kind {
		case "IMPORTS":
			imports[e.ToName] = true
		case "CALLS":
			calls[e.ToName] = true
		}
	}
	for _, want := range []string{"App\\Contracts\\Repository", "App\\Support\\Helper"} {
		if !imports[want] {
			t.Errorf("import %q missing; got %v", want, imports)
		}
	}
	for _, want := range []string{"init", "load", "record", "Helper"} {
		if !calls[want] {
			t.Errorf("CALLS edge to %q missing; got %v", want, calls)
		}
	}
}
