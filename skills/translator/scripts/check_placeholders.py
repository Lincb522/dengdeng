#!/usr/bin/env python3
"""Compare placeholder and lightweight markup tokens in two text files."""

from __future__ import annotations

import argparse
import re
from collections import Counter
from pathlib import Path


TOKEN = re.compile(
    r"\{\{[^{}]+\}\}|\$\{[^{}]+\}|\{[A-Za-z_][A-Za-z0-9_.-]*\}|"
    r"%(?:\d+\$)?[sdif]|</?[A-Za-z][^>]*>|`[^`\n]+`|https?://[^\s)\]>]+"
)


def tokens(path: Path, encoding: str) -> Counter[str]:
    return Counter(TOKEN.findall(path.read_text(encoding=encoding)))


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("source", type=Path)
    parser.add_argument("translation", type=Path)
    parser.add_argument("--encoding", default="utf-8")
    args = parser.parse_args()

    source = tokens(args.source, args.encoding)
    translation = tokens(args.translation, args.encoding)
    missing = source - translation
    added = translation - source
    if not missing and not added:
        print("placeholder check passed")
        return 0
    for token, count in sorted(missing.items()):
        print(f"missing\t{count}\t{token}")
    for token, count in sorted(added.items()):
        print(f"added\t{count}\t{token}")
    return 1


if __name__ == "__main__":
    raise SystemExit(main())
