#!/usr/bin/env python3
from __future__ import annotations

import argparse
import hashlib
import json
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
SOURCE = ROOT / "skills"
OUTPUT = ROOT / "backend" / "internal" / "service" / "creation_skill_packages.json"


def split_skill_markdown(text: str) -> tuple[dict[str, str], str]:
    if not text.startswith("---\n"):
        raise ValueError("SKILL.md is missing YAML frontmatter")
    end = text.find("\n---\n", 4)
    if end < 0:
        raise ValueError("SKILL.md frontmatter is not closed")
    metadata: dict[str, str] = {}
    for line in text[4:end].splitlines():
        key, separator, value = line.partition(":")
        if separator and key.strip() in {"name", "description"}:
            metadata[key.strip()] = value.strip().strip('"\'')
    return metadata, text[end + 5 :].strip()


def text_resources(directory: Path, kind: str) -> list[dict[str, str]]:
    root = directory / kind
    if not root.is_dir():
        return []
    resources: list[dict[str, str]] = []
    for path in sorted(item for item in root.rglob("*") if item.is_file()):
        content = path.read_text(encoding="utf-8")
        resources.append({"path": path.relative_to(directory).as_posix(), "content": content.strip()})
    return resources


def build() -> dict[str, object]:
    packages: list[dict[str, object]] = []
    for directory in sorted(item for item in SOURCE.iterdir() if item.is_dir()):
        skill_path = directory / "SKILL.md"
        if not skill_path.is_file():
            continue
        metadata, instructions = split_skill_markdown(skill_path.read_text(encoding="utf-8"))
        if metadata.get("name") != directory.name:
            raise ValueError(f"{skill_path}: name must match directory")
        package: dict[str, object] = {
            "id": directory.name,
            "name": metadata["name"],
            "description": metadata.get("description", ""),
            "instructions": instructions,
            "references": text_resources(directory, "references"),
            "scripts": text_resources(directory, "scripts"),
        }
        canonical = json.dumps(package, ensure_ascii=False, sort_keys=True, separators=(",", ":")).encode()
        package["revision"] = hashlib.sha256(canonical).hexdigest()
        packages.append(package)
    return {"version": 1, "packages": packages}


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--check", action="store_true")
    args = parser.parse_args()
    rendered = json.dumps(build(), ensure_ascii=False, indent=2) + "\n"
    if args.check:
        if not OUTPUT.is_file() or OUTPUT.read_text(encoding="utf-8") != rendered:
            print(f"out of date: {OUTPUT}")
            return 1
        print(f"skill packages are current: {OUTPUT}")
        return 0
    OUTPUT.write_text(rendered, encoding="utf-8")
    print(OUTPUT)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
