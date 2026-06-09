# Pincher + Goose benefits

Goose already ships a strong Developer extension for shell and text-editor work. Pincher complements that loop with local code-graph tools and a project-scoped Open Plugins hook bridge.

## What changes for a Goose user

| Goose default path | With Pincher |
|---|---|
| Ask the model to inspect code, then rely on broad shell/file reads. | Goose can call `pincher search` first and see ranked symbols, snippets, file paths, confidence, and token accounting. |
| Open whole files to understand one function. | Goose can call `pincher context` and get the selected symbol plus directly relevant callees/import context. |
| Grep for callers before a change. | Goose can call `pincher trace` and follow graph edges with risk labels. |
| Review a diff by rereading changed files manually. | Goose can call `pincher changes` and get changed symbols, impacted callers, and suggested tests. |
| Runtime steering is only prompt/policy text. | `pincher init --target=goose` adds a Goose Open Plugins `PreToolUse` hook for `developer__shell|developer__text_editor` and forwards hook payloads to `pincher hook-check`. |

## The two-piece setup

1. **MCP extension:** add Pincher as a Goose stdio extension so Goose can call Pincher tools.
2. **Open Plugins hook:** run `pincher init --target=goose` from the project root so Developer-tool calls pass through Pincher's hook guardrail.

The Goose tutorial has the full copy-paste setup: [`docs/tutorials/goose.md`](../../tutorials/goose.md).

## Why the hook matters

Instruction files help, but agent behavior is not guaranteed. The generated Goose hook creates a concrete runtime check before broad shell/editor actions:

```json
{
  "matcher": "developer__shell|developer__text_editor",
  "hooks": [
    {
      "type": "command",
      "command": "${PLUGIN_ROOT}/scripts/pincher-hook-check.sh"
    }
  ]
}
```

The hook script receives Goose's raw `PreToolUse` payload, logs a local debug copy, and pipes the event to `pincher hook-check`. This mirrors the Claude hook strategy while using Goose's Open Plugins layout under `.agents/plugins/pincher/`.

## What to verify

After setup, ask Goose:

> Use Pincher to find the payment processing entry point, then trace its callers before editing anything.

A healthy Goose session should prefer Pincher MCP tools such as `search`, `context`, and `trace` for code navigation. Then trigger a safe Developer-tool action and confirm `.agents/plugins/pincher/last-event.log` contains a `PreToolUse` event.
