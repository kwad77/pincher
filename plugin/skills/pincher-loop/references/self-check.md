# Are you ACTUALLY using this skill? (the self-check that stops overclaiming)

Labeling stages `[S3: Probe]` is **not** using the skill. The skill is the
*composite-first tooling*. It is easy — and a documented failure of the first
run of this skill — to narrate EGDL stage labels while still navigating with
`Read`/`Grep`/throwaway scripts. Guard against it:

- **Opening move must be a composite.** The **first** code-navigation action of
  any loop is `onboard_module` / `context_for_task` / `guide` — never a `Read` or
  `Grep`. If your first code action was a `Read`, you are not running this skill.
- **Trip-wire before every code `Read`/`Grep`.** Say, in one clause, which
  composite you considered and the *concrete* reason it doesn't fit. Only three
  reasons are valid: (a) test-assertion body, (b) non-code (build/CI/lockfile),
  (c) a composite already returned `_meta.empty_reason`. "It was faster to grep"
  is **not** valid — that is the habit the skill exists to break.
- **Root-cause means `investigate_failure`.** When a test/build fails, the Stage-4
  driver is `investigate_failure error_text=...`, not hand-written diagnostic
  scripts. Writing a throwaway test to bisect behavior is a fallback *after* the
  composite, not instead of it.
- **Tool ledger.** Keep a running tally for the loop: `composite calls` vs
  `code-nav Read/Grep calls`. If Read/Grep for code outnumbers composites, you
  have drifted off-skill — stop and name the composite you skipped.
- **Honesty gate (do not skip).** Before telling the user "I used pincher-loop",
  check the ledger. If composites did not drive navigation, say so plainly:
  "I followed the EGDL discipline but navigated with Read/Grep, not the
  composites" — never round that up to "used the skill." Precision here is the
  point; the skill's value is unprovable if its usage is overclaimed.

## The honest carve-out — when Read/Grep is correct

Pincher replaces *code navigation*, not everything. Read directly for:

- **Test assertion bodies** — to match exact conventions/QNs, you must read the
  test's expectations whole. `symbol` gives the function but you need the asserts.
- **Non-code**: build scripts, CI YAML, lockfiles, generated artifacts.
- A file pincher can't index, or exact-byte inspection (whitespace).

Don't pretend pincher covers these — naming the limit is what makes the rule
trustworthy rather than dogmatic.
