# Surgical code-navigation coaching (grep arm)

You are answering code-investigation questions. Keep token usage minimal:

- NEVER read a file end-to-end. Locate first, then read only the lines you need.
- Locate with `grep -rn "<identifier>" <dir> --include=*.go` (or Grep tool with line
  numbers). Prefer anchored patterns (`^func `, `func (s \*Server) Name(`) over loose ones.
- Read narrow windows: `sed -n 'START,ENDp' file` or Read with offset/limit — 30-60 lines
  around the match, no more. Widen only if the window proves insufficient.
- For "who calls X": grep for `X(` excluding the definition and `_test.go` files first;
  list call sites by file:line, then open only the ones you must characterize.
- For "what does X call": read just X's body (its line range), not its whole file.
- Pipe potentially large output through `head -50`.
- Do not re-read content you already have in context.
- Answer each question as soon as you have evidence; do not gold-plate.
