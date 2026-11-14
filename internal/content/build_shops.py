"""Extract the reviewed legacy NPC-shop catalog; never connect to a database."""
from __future__ import annotations
import argparse
import hashlib
import json
from pathlib import Path
import re

def rows(sql: str, table: str):
    for match in re.finditer(r"INSERT INTO `" + re.escape(table) + r"` VALUES (.*?);", sql, re.S):
        text = match.group(1)
        values, token, quoted, escaped, depth = [], [], False, False, 0
        for char in text:
            if escaped:
                token.append(char); escaped = False; continue
            if quoted and char == "\\":
                escaped = True; continue
            if char == "'":
                quoted = not quoted; token.append(char); continue
            if not quoted and char == "(":
                depth += 1; continue
            if not quoted and char in ",)":
                if depth == 0: continue
                part = "".join(token).strip(); token = []
                values.append(part[1:-1] if part.startswith("'") else None if part == "NULL" else int(part))
                if char == ")":
                    yield values; values = []; depth -= 1
                continue
            token.append(char)
        if quoted or depth:
            raise ValueError("unterminated SQL tuple")

def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("source", type=Path)
    parser.add_argument("--output", type=Path, default=Path(__file__).with_name("npc_shops.json"))
    args = parser.parse_args()
    raw = args.source.read_bytes()
    sql = raw.decode("utf-8")
    result = {"version": 1, "source": args.source.name, "source_sha256": hashlib.sha256(raw).hexdigest(), "shops": list(rows(sql, "shops")), "items": list(rows(sql, "shopitems")), "ranks": list(rows(sql, "shopranks"))}
    if not result["shops"] or not result["items"]:
        raise ValueError("source has no catalog")
    args.output.write_text(json.dumps(result, ensure_ascii=False, separators=(",", ":")) + "\n", encoding="utf-8")
    print(json.dumps({"shops": len(result["shops"]), "items": len(result["items"]), "ranks": len(result["ranks"]), "source_sha256": result["source_sha256"]}))

if __name__ == "__main__":
    main()
