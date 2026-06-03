// SPDX-License-Identifier: MIT

package server

import (
	"testing"
)

// #912: threshold/comparison audit phrasings ("find every function
// with complexity above 50") must route to shapeAudit so guide
// recommends pinchQL, not BM25 search. Same structural-audit intent
// as the absence pattern in #608, just on a metric rather than
// presence/absence.

func TestClassifyTaskShape_AuditThresholdPattern(t *testing.T) {
	t.Parallel()
	cases := []string{
		"find every function with complexity above 50",
		"list all methods with more than 100 lines",
		"find every class with cyclomatic complexity over 30",
		"count every function whose complexity exceeds 25",
		"show all methods having more than 5 parameters",
		"find any function with complexity greater than 20",
		"list all symbols with complexity below 5",
		"find every method whose length is less than 10",
		"surface all functions with complexity at least 100",
		"find any function with complexity >= 50",
		"show every function with complexity > 50",
	}
	for _, task := range cases {
		t.Run(task, func(t *testing.T) {
			got := classifyTaskShape(task)
			if got != shapeAudit {
				t.Errorf("classifyTaskShape(%q) = %v, want shapeAudit", task, got)
			}
		})
	}
}

// Control: tasks that look threshold-ish but aren't structural audits
// must not get pulled into shapeAudit.
func TestClassifyTaskShape_AuditThreshold_NotOverbroad(t *testing.T) {
	t.Parallel()
	cases := []struct {
		task     string
		notWant  guideShape
		toleratedShapes []guideShape
	}{
		// Bare property reference without "find/list/every" prefix: not
		// our pattern's territory.
		{
			task:    "complexity above 50",
			notWant: shapeAudit,
		},
		// "above" alone is a position, not a comparison — should remain
		// search/find-shape.
		{
			task:    "find the function above the import block",
			notWant: shapeAudit,
		},
	}
	for _, c := range cases {
		t.Run(c.task, func(t *testing.T) {
			got := classifyTaskShape(c.task)
			if got == c.notWant {
				t.Errorf("classifyTaskShape(%q) = %v, must not match %v", c.task, got, c.notWant)
			}
		})
	}
}
