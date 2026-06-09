// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// branch_overlap.go — `branch_overlap` answers a question `changes`
// can't: given two branches in flight, do they touch the same code?
// It diffs each branch against a shared base, maps the changed files
// to symbols, and intersects the two sets. A non-empty intersection is
// merge-order risk — whichever branch merges second is now sitting on
// changed foundations and needs re-review.
//
// `changes scope=base:<branch>` already previews ONE branch's blast
// radius; this composes the same idea for a PAIR and reports where
// they collide. Deterministic: git diff + pincher's file→symbol index,
// no inference.

// gitChangedFilesBetween returns the repo-relative files that `ref`
// changed since it diverged from `base` (three-dot diff = merge-base
// semantics, the same "what does this branch introduce" question
// `changes scope=base:` asks).
func gitChangedFilesBetween(root, base, ref string) ([]string, error) {
	cmd := exec.Command("git", "diff", "--name-only", base+"..."+ref)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var files []string
	for _, line := range strings.Split(string(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			files = append(files, line)
		}
	}
	return files, nil
}

// gitChangedHunksBetween returns added-side line ranges for the diff a
// branch introduces relative to base. It mirrors changes' hunk parsing so
// branch_overlap can report symbols actually touched by each branch instead
// of every symbol that happens to live in a shared file.
func gitChangedHunksBetween(root, base, ref string) (map[string][][2]int, error) {
	cmd := exec.Command("git", "diff", "--unified=0", base+"..."+ref)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return parseGitDiffHunks(string(out)), nil
}

// gitMergeBase returns the merge-base commit of two refs.
func gitMergeBase(root, a, b string) (string, error) {
	cmd := exec.Command("git", "merge-base", a, b)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git merge-base %s %s: %w", a, b, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// verifyGitRef checks a ref both passes name validation and exists.
func verifyGitRef(root, ref string) error {
	if err := validateGitRefName(ref); err != nil {
		return fmt.Errorf("invalid ref %q: %w", ref, err)
	}
	cmd := exec.Command("git", "rev-parse", "--verify", "--quiet", ref)
	cmd.Dir = root
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ref %q not found in the repo", ref)
	}
	return nil
}

func (s *Server) handleBranchOverlap(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	start, tool, args := beginCall(req)
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	branchA := str(args, "branch_a")
	branchB := str(args, "branch_b")
	if branchA == "" || branchB == "" {
		return s.errResultRich(
			"branch_overlap requires both `branch_a` and `branch_b`",
			[]map[string]string{
				{"tool": "branch_overlap", "args": `{"branch_a":"feature/x","branch_b":"feature/y"}`,
					"why": "pass two in-flight branches to check whether they touch the same symbols"},
			},
		), nil
	}

	projectID, err := s.resolveProjectID(str(args, "project"))
	if err != nil {
		return s.errResultRich(err.Error(), []map[string]string{
			{"tool": "list", "args": `{}`, "why": "see every indexed project with its id + on-disk path"},
		}), nil
	}
	root, err := s.resolveProjectRoot(projectID)
	if err != nil {
		return errResult(err.Error()), nil
	}

	for _, ref := range []string{branchA, branchB} {
		if err := verifyGitRef(root, ref); err != nil {
			return s.errResultRich(fmt.Sprintf("branch_overlap: %v", err), []map[string]string{
				{"tool": "branch_overlap", "args": `{"branch_a":"master","branch_b":"HEAD"}`,
					"why": "pass branch names or commit-ish refs that exist in this repo"},
			}), nil
		}
	}

	base := str(args, "base")
	if base == "" {
		base, err = gitMergeBase(root, branchA, branchB)
		if err != nil {
			return errResult(fmt.Sprintf("branch_overlap: %v", err)), nil
		}
	} else if err := verifyGitRef(root, base); err != nil {
		return errResult(fmt.Sprintf("branch_overlap: %v", err)), nil
	}

	filesA, err := gitChangedFilesBetween(root, base, branchA)
	if err != nil {
		return errResult(fmt.Sprintf("branch_overlap: diff %s: %v", branchA, err)), nil
	}
	filesB, err := gitChangedFilesBetween(root, base, branchB)
	if err != nil {
		return errResult(fmt.Sprintf("branch_overlap: diff %s: %v", branchB, err)), nil
	}
	hunksA, hunkErrA := gitChangedHunksBetween(root, base, branchA)
	hunksB, hunkErrB := gitChangedHunksBetween(root, base, branchB)

	overlap := computeBranchOverlap(s, projectID, filesA, filesB, hunksA, hunksB)
	overlap.BranchA = branchA
	overlap.BranchB = branchB
	overlap.Base = base

	data := map[string]any{
		"branch_a":            branchA,
		"branch_b":            branchB,
		"base":                base,
		"branch_a_file_count": len(filesA),
		"branch_b_file_count": len(filesB),
		"overlapping_files":   overlap.OverlappingFiles,
		"overlapping_symbols": overlap.OverlappingSymbols,
		"summary": map[string]any{
			"overlapping_files":         len(overlap.OverlappingFiles),
			"overlapping_symbols":       overlap.OverlappingSymbolCount,
			"overlapping_symbols_shown": len(overlap.OverlappingSymbols),
		},
		"verdict": overlap.Verdict,
	}
	meta := map[string]any{}
	if hunkErrA != nil || hunkErrB != nil {
		meta["warnings"] = []string{fmt.Sprintf("branch_overlap fell back to file-granular symbols because hunk diff failed (branch_a: %v; branch_b: %v)", hunkErrA, hunkErrB)}
	}
	if overlap.SymbolsTrimmed {
		existing, _ := meta["warnings"].([]string)
		meta["warnings"] = append(existing, fmt.Sprintf("overlapping_symbols trimmed to %d of %d — see summary.overlapping_symbols for the full count", len(overlap.OverlappingSymbols), overlap.OverlappingSymbolCount))
	}
	if len(meta) > 0 {
		data["_meta"] = meta
	}
	return s.jsonResultWithMeta(data, start, tool, args, 0), nil
}

// branchOverlapResult is the computed overlap between two branches.
type branchOverlapResult struct {
	BranchA                string
	BranchB                string
	Base                   string
	OverlappingFiles       []string
	OverlappingSymbols     []string
	OverlappingSymbolCount int
	SymbolsTrimmed         bool
	Verdict                string
}

// computeBranchOverlap intersects two branches' changed-file sets and
// the symbol sets those files contain, then derives a merge-order
// verdict. Split out from the handler so it's unit-testable against a
// store without spawning git.
func computeBranchOverlap(s *Server, projectID string, filesA, filesB []string, hunksA, hunksB map[string][][2]int) branchOverlapResult {
	setA := make(map[string]bool, len(filesA))
	for _, f := range filesA {
		setA[f] = true
	}
	overlapFiles := []string{}
	for _, f := range filesB {
		if setA[f] {
			overlapFiles = append(overlapFiles, f)
		}
	}
	sort.Strings(overlapFiles)

	// Symbols in the overlapping files are the candidates for a
	// semantic (not merely textual) collision. When hunk ranges are
	// available, a symbol counts as shared only when BOTH branches'
	// diffs touch that symbol's line range. If hunk parsing failed for a
	// file, fall back to the older file-granular behavior so the safety
	// signal over-reports rather than hiding a real collision.
	symSet := map[string]bool{}
	for _, f := range overlapFiles {
		syms, err := s.store.GetSymbolsForFile(projectID, f)
		if err != nil {
			continue
		}
		fileHunksA, hasHunksA := hunksA[f]
		fileHunksB, hasHunksB := hunksB[f]
		for _, sym := range syms {
			if hasHunksA && len(fileHunksA) > 0 && !symbolOverlapsHunks(sym.StartLine, sym.EndLine, fileHunksA) {
				continue
			}
			if hasHunksB && len(fileHunksB) > 0 && !symbolOverlapsHunks(sym.StartLine, sym.EndLine, fileHunksB) {
				continue
			}
			symSet[sym.ID] = true
		}
	}
	overlapSymbols := make([]string, 0, len(symSet))
	for id := range symSet {
		overlapSymbols = append(overlapSymbols, id)
	}
	sort.Strings(overlapSymbols)
	fullSymbolCount := len(overlapSymbols)
	symbolsTrimmed := false
	if len(overlapSymbols) > changesMaxList {
		overlapSymbols = overlapSymbols[:changesMaxList]
		symbolsTrimmed = true
	}

	var verdict string
	switch {
	case len(overlapFiles) == 0:
		verdict = "independent — the two branches change disjoint files; merge order does not matter."
	case fullSymbolCount == 0:
		verdict = fmt.Sprintf("low risk — %d shared file(s) but no indexed symbols in them (config/docs); a textual merge conflict is possible, a semantic one is not.", len(overlapFiles))
	default:
		verdict = fmt.Sprintf("merge-order risk — the branches share %d file(s) and %d touched symbol(s). Whichever merges second is sitting on changed foundations; re-review it against the first before merging.", len(overlapFiles), fullSymbolCount)
	}

	return branchOverlapResult{
		OverlappingFiles:       overlapFiles,
		OverlappingSymbols:     overlapSymbols,
		OverlappingSymbolCount: fullSymbolCount,
		SymbolsTrimmed:         symbolsTrimmed,
		Verdict:                verdict,
	}
}
