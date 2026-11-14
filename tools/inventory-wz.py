"""Create a reproducible hash inventory of the read-only WZ XML source tree."""
from __future__ import annotations

import argparse
from collections import Counter
import hashlib
import json
from pathlib import Path


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source-root", required=True, type=Path)
    parser.add_argument("--output", required=True, type=Path)
    args = parser.parse_args()
    root = args.source_root.resolve(strict=True)
    if not root.is_dir():
        parser.error("source-root must be a directory")
    rows = []
    categories: Counter[str] = Counter()
    combined = hashlib.sha256()
    for path in sorted(root.rglob("*.xml")):
        relative = path.relative_to(root).as_posix()
        digest = hashlib.sha256()
        size = 0
        with path.open("rb") as stream:
            while block := stream.read(1024 * 1024):
                digest.update(block)
                size += len(block)
        checksum = digest.hexdigest()
        combined.update(relative.encode("utf-8") + b"\0" + checksum.encode("ascii") + b"\n")
        categories[relative.split("/", 1)[0]] += 1
        rows.append({"path": relative, "bytes": size, "sha256": checksum})
    if not rows:
        parser.error("source-root contains no WZ XML files")
    data = {"schema_version": 1, "source_root": str(root),
            "method": "SHA256 of sorted UTF-8 relative path + NUL + content SHA256 + LF",
            "inventory_sha256": combined.hexdigest(), "file_count": len(rows),
            "total_bytes": sum(row["bytes"] for row in rows),
            "categories": dict(sorted(categories.items())), "files": rows}
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(data, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print(json.dumps({key: value for key, value in data.items() if key != "files"}, ensure_ascii=False))


if __name__ == "__main__":
    main()
