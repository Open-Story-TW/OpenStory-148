"""Extract legacy mob/global drop rows without connecting to the source DB."""
from __future__ import annotations

import argparse
import hashlib
import json
from pathlib import Path

from build_shops import rows


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("source", type=Path)
    parser.add_argument("--output", type=Path, default=Path(__file__).with_name("mob_drops.json"))
    args = parser.parse_args()
    raw = args.source.read_bytes()
    sql = raw.decode("utf-8")
    result = {"version": 1, "source": args.source.name,
              "source_sha256": hashlib.sha256(raw).hexdigest(),
              "drops": list(rows(sql, "drop_data")),
              "global": list(rows(sql, "drop_data_global"))}
    if not result["drops"]:
        raise ValueError("source has no mob drop data")
    args.output.write_text(json.dumps(result, ensure_ascii=False, separators=(",", ":")) + "\n", encoding="utf-8")
    print(json.dumps({"drops": len(result["drops"]), "global": len(result["global"]),
                      "source_sha256": result["source_sha256"]}))


if __name__ == "__main__":
    main()
