#!/usr/bin/env python3
"""
add-spdx-headers.py — add `// SPDX-License-Identifier: MIT` to every .go file
that doesn't already have one.

Convention used: SPDX line lives at the very top of the file, except when the
file leads with a `//go:build` constraint (which must be the first directive
on the file per Go build semantics). In the build-tag case the SPDX line goes
AFTER the build-tag block + its blank-line separator.

Idempotent: a second run is a no-op (every file is checked for an existing
`SPDX-License-Identifier:` substring before insertion).

Usage:
    python3 scripts/add-spdx-headers.py            # apply to every .go file
    python3 scripts/add-spdx-headers.py --dry-run  # report what would change
"""

from __future__ import annotations

import argparse
import os
import sys
from pathlib import Path

SPDX_LINE = "// SPDX-License-Identifier: MIT\n"
SKIP_DIRS = {".git", "vendor", "node_modules"}


def should_skip(path: Path) -> bool:
    parts = set(path.parts)
    return bool(parts & SKIP_DIRS)


def find_go_files(root: Path) -> list[Path]:
    out: list[Path] = []
    for dirpath, dirnames, filenames in os.walk(root):
        # In-place prune to avoid descending into skip dirs.
        dirnames[:] = [d for d in dirnames if d not in SKIP_DIRS]
        for fn in filenames:
            if fn.endswith(".go"):
                out.append(Path(dirpath) / fn)
    return out


def already_has_spdx(text: str) -> bool:
    # Only check the first ~10 lines; SPDX must be near the top to count.
    head = "\n".join(text.splitlines()[:10])
    return "SPDX-License-Identifier:" in head


def insert_spdx(text: str) -> str:
    lines = text.splitlines(keepends=True)

    # Find the end of any leading //go:build block (Go requires it at the top).
    i = 0
    in_build_block = False
    while i < len(lines):
        stripped = lines[i].lstrip()
        if stripped.startswith("//go:build") or stripped.startswith("// +build"):
            in_build_block = True
            i += 1
            continue
        if in_build_block and stripped == "":
            # Consume the blank line that separates build tag from package.
            i += 1
            break
        break

    head = lines[:i]
    tail = lines[i:]

    new_lines = head + [SPDX_LINE, "\n"] + tail
    return "".join(new_lines)


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--dry-run", action="store_true")
    ap.add_argument("--root", default=".", type=Path)
    args = ap.parse_args()

    root = args.root.resolve()
    go_files = find_go_files(root)
    go_files = [p for p in go_files if not should_skip(p.relative_to(root))]

    changed = 0
    skipped = 0
    for path in go_files:
        text = path.read_text()
        if already_has_spdx(text):
            skipped += 1
            continue
        new_text = insert_spdx(text)
        if new_text == text:
            skipped += 1
            continue
        if not args.dry_run:
            path.write_text(new_text)
        changed += 1

    label = "would change" if args.dry_run else "changed"
    print(f"{label}: {changed}    already-have-spdx: {skipped}    total: {len(go_files)}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
