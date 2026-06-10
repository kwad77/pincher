// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// #1082: the `guide` prompt is the user-controlled (slash-command) twin of
// the `guide` tool. These tests exercise the wire path through the in-memory
// MCP transport: prompts/list must surface `guide`, and prompts/get must
// return the same shape-driven recommendations the tool produces.

func TestGuidePrompt_ListedOverWire(t *testing.T) {
	srv, _, _ := newTestServer(t)
	cs, cleanup := connectInMemoryClient(t, srv, nil)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, err := cs.ListPrompts(ctx, nil)
	if err != nil {
		t.Fatalf("ListPrompts: %v", err)
	}
	var found *mcp.Prompt
	for _, p := range res.Prompts {
		if p.Name == guidePromptName {
			found = p
			break
		}
	}
	if found == nil {
		t.Fatalf("prompts/list did not include %q; got %d prompts", guidePromptName, len(res.Prompts))
	}
	// The required `task` argument must be declared so a host can prompt
	// the user for it.
	var hasTask bool
	for _, a := range found.Arguments {
		if a.Name == "task" {
			hasTask = true
			if !a.Required {
				t.Errorf("guide prompt `task` argument should be required")
			}
		}
	}
	if !hasTask {
		t.Errorf("guide prompt missing the `task` argument")
	}
}

func TestGuidePrompt_GetReturnsRecommendations(t *testing.T) {
	srv, _, _ := newTestServer(t)
	cs, cleanup := connectInMemoryClient(t, srv, nil)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	const task = "fix the login retry bug"
	res, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{
		Name:      guidePromptName,
		Arguments: map[string]string{"task": task},
	})
	if err != nil {
		t.Fatalf("GetPrompt: %v", err)
	}
	if len(res.Messages) == 0 {
		t.Fatalf("guide prompt returned no messages")
	}
	tc, ok := res.Messages[0].Content.(*mcp.TextContent)
	if !ok {
		t.Fatalf("message content is %T, want *mcp.TextContent", res.Messages[0].Content)
	}
	if res.Messages[0].Role != "user" {
		t.Errorf("message role = %q, want %q", res.Messages[0].Role, "user")
	}
	// The rendered prompt must echo the task and name at least one
	// recommended pincher tool. computeGuide is the shared source of truth
	// with the tool, so we only assert the surface contract here.
	if !strings.Contains(tc.Text, task) {
		t.Errorf("prompt text did not echo the task; got:\n%s", tc.Text)
	}
	if !strings.Contains(tc.Text, "Recommended pincher tool calls") {
		t.Errorf("prompt text missing the recommendations header; got:\n%s", tc.Text)
	}
}

// TestGuidePrompt_ParityWithTool pins that the prompt and the tool share the
// same recommendation core: every tool the prompt text names must come from
// computeGuide, which the tool also calls. We assert by comparing the
// prompt's recommended tools against a direct computeGuide call.
func TestGuidePrompt_ParityWithTool(t *testing.T) {
	srv, _, _ := newTestServer(t)

	const task = "understand how indexing handles symlinks"
	_, _, recs, _ := srv.computeGuide(task, "")
	if len(recs) == 0 {
		t.Fatalf("computeGuide returned no recommendations for %q", task)
	}

	res, err := srv.handleGuidePrompt(context.Background(), &mcp.GetPromptRequest{
		Params: &mcp.GetPromptParams{
			Name:      guidePromptName,
			Arguments: map[string]string{"task": task},
		},
	})
	if err != nil {
		t.Fatalf("handleGuidePrompt: %v", err)
	}
	text := res.Messages[0].Content.(*mcp.TextContent).Text
	for _, rec := range recs {
		if tool := rec["tool"]; tool != "" && !strings.Contains(text, tool) {
			t.Errorf("prompt text missing recommended tool %q from computeGuide; got:\n%s", tool, text)
		}
	}
}

func TestGuidePrompt_EmptyTaskErrors(t *testing.T) {
	srv, _, _ := newTestServer(t)

	_, err := srv.handleGuidePrompt(context.Background(), &mcp.GetPromptRequest{
		Params: &mcp.GetPromptParams{
			Name:      guidePromptName,
			Arguments: map[string]string{"task": "   "},
		},
	})
	if err == nil {
		t.Fatal("handleGuidePrompt with empty task should error")
	}
	if !strings.Contains(err.Error(), "task") {
		t.Errorf("error should mention the missing `task` argument; got: %v", err)
	}
}
