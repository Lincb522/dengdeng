#!/usr/bin/env python3
"""Profile CSV structure and numeric ranges without printing row values."""

from __future__ import annotations

import argparse
import csv
import json
import math
from collections import Counter
from pathlib import Path
from typing import Optional


def parse_number(value: str) -> Optional[float]:
    try:
        number = float(value)
    except ValueError:
        return None
    return number if math.isfinite(number) else None


def profile(path: Path, encoding: str) -> dict[str, object]:
    with path.open("r", encoding=encoding, newline="") as handle:
        reader = csv.DictReader(handle)
        if not reader.fieldnames:
            raise ValueError("CSV header is missing")
        names = [name.strip() for name in reader.fieldnames]
        if any(not name for name in names):
            raise ValueError("CSV contains an empty column name")
        if len(set(names)) != len(names):
            raise ValueError("CSV contains duplicate column names")

        stats = {
            name: {
                "non_empty": 0,
                "missing": 0,
                "numeric": 0,
                "numeric_min": None,
                "numeric_max": None,
                "numeric_sum": 0.0,
                "values": Counter(),
                "unique_overflow": False,
            }
            for name in names
        }
        row_count = 0
        for row in reader:
            row_count += 1
            for original, name in zip(reader.fieldnames, names):
                value = (row.get(original) or "").strip()
                column = stats[name]
                if value == "":
                    column["missing"] += 1
                    continue
                column["non_empty"] += 1
                if not column["unique_overflow"]:
                    column["values"][value] += 1
                    if len(column["values"]) > 10_000:
                        column["values"].clear()
                        column["unique_overflow"] = True
                number = parse_number(value)
                if number is not None:
                    column["numeric"] += 1
                    column["numeric_sum"] += number
                    current_min = column["numeric_min"]
                    current_max = column["numeric_max"]
                    column["numeric_min"] = number if current_min is None else min(current_min, number)
                    column["numeric_max"] = number if current_max is None else max(current_max, number)

    columns = []
    for name in names:
        column = stats[name]
        numeric_count = column["numeric"]
        columns.append(
            {
                "name": name,
                "non_empty": column["non_empty"],
                "missing": column["missing"],
                "unique": None if column["unique_overflow"] else len(column["values"]),
                "numeric_count": numeric_count,
                "numeric_min": column["numeric_min"],
                "numeric_max": column["numeric_max"],
                "numeric_mean": column["numeric_sum"] / numeric_count if numeric_count else None,
            }
        )
    return {"file": str(path), "rows": row_count, "columns": columns}


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("csv_file", type=Path)
    parser.add_argument("--encoding", default="utf-8-sig")
    parser.add_argument("--compact", action="store_true")
    args = parser.parse_args()
    result = profile(args.csv_file, args.encoding)
    print(json.dumps(result, ensure_ascii=False, indent=None if args.compact else 2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
