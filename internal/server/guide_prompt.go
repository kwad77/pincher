// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// guidePromptName is the registered name of the `guide` MCP prompt. It
// matches the `guide` tool name on purpose: the prompt is the
// user-controlled (slash-command) surface of the same capability the tool
// exposes to the model. Hosts list it under prompts/list and invoke it via
// prompts/get.
const guidePromptName = "guide"

// registerPrompts declares pincher's MCP prompts (#1082). Registering any
// prompt makes the SDK advertise the `prompts` capability in the initialize
// response, so the host shows these as slash commands. Today there is one:
// `guide`, the user-controlled twin of the `guide` tool.
func (s *Server) registerPrompts() {
	s.mcp.AddPrompt(&mcp.Prompt{
		Name:        guidePromptName,
		Title:       "Pincher: recommend tools for a task",
		Description: "Given a free-form task description, returns the 2-3 pincher tool calls to run next. Use when you're unsure where to start.",
		Arguments: []*mcp.PromptArgument{
			{
				Name:        "task",
				Title:       "Task",
				Description: "Free-form description of what you're trying to do (e.g. \"fix the login retry bug\").",
				Required:    true,
			},
			{
				Name:        "project",
				Title:       "Project",
				Description: "Optional project name or ID to scope the hint. Defaults to the session project.",
				Required:    false,
			},
		},
	}, s.handleGuidePrompt)
}

// handleGuidePrompt serves prompts/get for the `guide` prompt (#1082). It
// reuses the exact recommendation core as the `guide` tool (computeGuide) so
// the two surfaces never drift, then renders the recommendations as a single
// user-role message the host injects into the conversation.
func (s *Server) handleGuidePrompt(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	var task, project string
	if req != nil && req.Params != nil {
		task = strings.TrimSpace(req.Params.Arguments["task"])
		project = strings.TrimSpace(req.Params.Arguments["project"])
	}
	if task == "" {
		// Prompts have no _meta error envelope; the contract is a plain
		// error from the handler. Mirror the tool's pedagogy in the text.
		return nil, fmt.Errorf("the %q prompt requires a non-empty %q argument (a free-form description of what you're trying to do, e.g. \"fix the login retry bug\")", guidePromptName, "task")
	}

	shape, hint, recommendations, projectWarning := s.computeGuide(task, project)

	var b strings.Builder
	fmt.Fprintf(&b, "Task: %s\n", task)
	fmt.Fprintf(&b, "Detected task shape: %s\n", shape)
	fmt.Fprintf(&b, "Search hint: %s\n\n", hint)
	b.WriteString("Recommended pincher tool calls, in order:\n")
	for i, rec := range recommendations {
		tool := rec["tool"]
		args := rec["args"]
		why := rec["why"]
		fmt.Fprintf(&b, "%d. `%s`", i+1, tool)
		if args != "" {
			fmt.Fprintf(&b, " with args `%s`", args)
		}
		if why != "" {
			fmt.Fprintf(&b, " — %s", why)
		}
		b.WriteString("\n")
	}
	b.WriteString("\nRun these calls to make progress on the task.")
	if projectWarning != "" {
		fmt.Fprintf(&b, "\n\nNote: %s", projectWarning)
	}

	return &mcp.GetPromptResult{
		Description: fmt.Sprintf("Recommended pincher tool calls for: %s", task),
		Messages: []*mcp.PromptMessage{
			{
				Role:    "user",
				Content: &mcp.TextContent{Text: b.String()},
			},
		},
	}, nil
}
