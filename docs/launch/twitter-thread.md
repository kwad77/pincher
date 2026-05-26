# v1.0 Twitter / X thread draft

Draft for [#1538](https://github.com/kwad77/pincher/issues/1538). Post after
the v1.0 release, announcement post, and demo links are public.

## Thread

1. Pincher 1.0 is out: local code intelligence for coding agents.

   It indexes symbols, relationships, source byte ranges, and search corpora so
   agents can ask targeted questions before reading whole files.

   Blog: `<<blog_url>>`

2. The 1.0 promise is stability. Tool schemas, the `_meta` envelope conventions,
   host setup docs, CLI workflows, and migration behavior are documented and
   versioned.

   Frozen surface ADR:
   `https://github.com/kwad77/pincher/blob/master/docs/adr/0002-v1-frozen-surface.md`

3. The basic loop is small:

   install -> setup host -> index repo -> ask `search`, `context`, `trace`, or a
   composite tool before broad file reads.

   Fresh-clone demo: `<<fresh_clone_demo_url>>`

4. The edit loop is where it matters:

   `plan_change` before editing, focused context while editing,
   `investigate_failure` when a test fails.

   Edit-confidence demo: `<<edit_confidence_demo_url>>`

5. Pincher stays local by design: one binary, local MCP server, local SQLite
   store. No hosted index required for the v1.0 workflow.

6. What is not in 1.0: plugin extractors, shared team indexes, and new MCP
   protocol capabilities like resources/completions/icons. Those stay in 1.x
   work so the 1.0 surface can be stable.

7. Install paths and host walkthroughs are in the README:

   `https://github.com/kwad77/pincher#readme`

8. Issues and feedback:

   `https://github.com/kwad77/pincher/issues`

## Publish-Time Checks

- Resolve every publish-time slot listed in `docs/launch/placeholder-audit.md`.
- Keep each post under the platform character limit after URLs are shortened.
- Do not add dollar figures or named-product comparisons.
- Include the demo videos only after they are public.
