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

	overlap := computeBranchOverlap(s, projectID, filesA, filesB)
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
		"verdict":             overlap.Verdict,
	}
	return s.jsonResultWithMeta(data, start, tool, args, 0), nil
}

// branchOverlapResult is the computed overlap between two branches.
type branchOverlapResult struct {
	BranchA            string
	BranchB            string
	Base               string
	OverlappingFiles   []string
	OverlappingSymbols []string
	Verdict            string
}

// computeBranchOverlap intersects two branches' changed-file sets and
// the symbol sets those files contain, then derives a merge-order
// verdict. Split out from the handler so it's unit-testable against a
// store without spawning git.
func computeBranchOverlap(s *Server, projectID string, filesA, filesB []string) branchOverlapResult {
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
	// semantic (not merely textual) collision. A symbol counts as
	// shared only when BOTH branches' diffs include its file.
	symSet := map[string]bool{}
	for _, f := range overlapFiles {
		syms, err := s.store.GetSymbolsForFile(projectID, f)
		if err != nil {
			continue
		}
		for _, sym := range syms {
			symSet[sym.ID] = true
		}
	}
	overlapSymbols := make([]string, 0, len(symSet))
	for id := range symSet {
		overlapSymbols = append(overlapSymbols, id)
	}
	sort.Strings(overlapSymbols)

	var verdict string
	switch {
	case len(overlapFiles) == 0:
		verdict = "independent — the two branches change disjoint files; merge order does not matter."
	case len(overlapSymbols) == 0:
		verdict = fmt.Sprintf("low risk — %d shared file(s) but no indexed symbols in them (config/docs); a textual merge conflict is possible, a semantic one is not.", len(overlapFiles))
	default:
		verdict = fmt.Sprintf("merge-order risk — the branches share %d file(s) and %d symbol(s). Whichever merges second is sitting on changed foundations; re-review it against the first before merging.", len(overlapFiles), len(overlapSymbols))
	}

	return branchOverlapResult{
		OverlappingFiles:   overlapFiles,
		OverlappingSymbols: overlapSymbols,
		Verdict:            verdict,
	}
}
