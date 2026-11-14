"""Read-only legacy SQL/source inventory for planning; never connects to a DB."""
from __future__ import annotations

import argparse
import hashlib
import json
import re
from datetime import datetime, timezone
from pathlib import Path


def sha256(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def main() -> None:
    project = Path(__file__).resolve().parents[1]
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--legacy-root", type=Path,
                        default=project.parent / "MapleStory_Legacy_Server")
    args = parser.parse_args()
    legacy = args.legacy_root.resolve(strict=True)
    source = legacy / "sql" / "empty_145_20250331.sql"
    sql = source.read_text(encoding="utf-8-sig")
    captured_at = datetime.now(timezone.utc).isoformat()
    tables = []
    for match in re.finditer(r"^CREATE TABLE `([^`]+)` \(\r?\n(.*?)^\) ENGINE=",
                             sql, re.MULTILINE | re.DOTALL):
        body = match.group(2)
        column_names = re.findall(r"^  `([^`]+)`", body, re.MULTILINE)
        tables.append({
            "table": match.group(1),
            "source_line": sql.count("\n", 0, match.start()) + 1,
            "column_count": len(column_names),
            "columns": column_names,
            "primary_key": re.findall(r"^  PRIMARY KEY (.*)", body, re.MULTILINE),
            "unique_indexes": re.findall(r"^  UNIQUE KEY (.*)", body, re.MULTILINE),
            "foreign_keys": re.findall(r"^  CONSTRAINT (.*)", body, re.MULTILINE),
            "migration_disposition": "pending_business_mapping",
        })
    if not tables or len({row["table"] for row in tables}) != len(tables):
        raise ValueError("Empty or duplicate table inventory; inspect SQL format.")

    output = project / "docs" / "baseline"
    output.mkdir(parents=True, exist_ok=True)
    inventory = {
        "captured_at_utc": captured_at,
        "legacy_root": str(legacy),
        "sql_source": str(source.relative_to(legacy)),
        "sql_sha256": sha256(source),
        "table_count": len(tables),
        "column_count": sum(row["column_count"] for row in tables),
        "scope": "DDL inventory only; no database reads or row export",
        "tables": tables,
    }
    evidence_paths = [
        "src/client/MapleClient.java", "src/client/MapleCharacter.java",
        "src/client/inventory/ItemLoader.java",
        "src/handling/channel/handler/InterServerHandler.java",
        "src/handling/world/World.java", "src/server/Start.java",
        "src/server/ShutdownServer.java", "src/tools/packet/LoginPacket.java",
        "src/tools/packet/PacketHelper.java",
    ]
    evidence = {
        "captured_at_utc": captured_at,
        "legacy_root": str(legacy),
        "files": [{"path": name, "sha256": sha256(legacy / name)}
                  for name in evidence_paths],
    }
    for name, content in [("legacy-schema.json", inventory),
                          ("source-evidence.json", evidence)]:
        (output / name).write_text(json.dumps(content, ensure_ascii=False, indent=2)
                                  + "\n", encoding="utf-8")
    print(json.dumps({"tables": inventory["table_count"],
                      "columns": inventory["column_count"],
                      "output": str(output)}, ensure_ascii=False))


if __name__ == "__main__":
    main()
