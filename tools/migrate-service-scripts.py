"""Convert exact, unconditional legacy service and numeric-warp wrappers to Lua.

This is deliberately not a JavaScript transpiler. A whole-source match is
required after lexical comment removal. Conditionals, other host calls,
strings, and unrecognized callback bodies are retained for manual migration.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import re
from pathlib import Path


def without_comments(text: str) -> str:
    out: list[str] = []
    i = 0
    while i < len(text):
        if text.startswith("//", i):
            end = text.find("\n", i + 2)
            i = len(text) if end < 0 else end
        elif text.startswith("/*", i):
            end = text.find("*/", i + 2)
            if end < 0:
                raise ValueError("unterminated source comment")
            out.append(" " + "\n" * text[i:end + 2].count("\n"))
            i = end + 2
        elif text[i] in "\"'`":
            # Preserve quoted data so it cannot become a falsely recognized
            # host call. The accepted wrapper grammar has no string literals.
            quote = text[i]
            start = i
            i += 1
            while i < len(text):
                if text[i] == "\\":
                    i += 2
                elif text[i] == quote:
                    i += 1
                    break
                else:
                    i += 1
            out.append(text[start:i])
        else:
            out.append(text[i])
            i += 1
    return "".join(out)


WRAPPER = re.compile(
    r"(?:varstatus=-?\d+;)?"
    r"(?:functionstart\(\)\{status=-1;action\(1,0,0\);\})?"
    r"function(?:start\(\)|action\(mode,type,selection\))\{"
    r"cm\.(?P<call>sendStorage\(\)|openShopNPC\((?P<shop>0|[1-9]\d*)\));"
    r"cm\.dispose\(\);\}"
)

PORTAL = re.compile(
    r"functionenter\(pi\)\{pi\.warp\((?P<map>0|[1-9]\d*)"
    r"(?:,(?P<portal>0|[1-9]\d*))?\);?(?:returntrue;?)?\}"
)

# Preserve whitespace inside quoted names. The numeric matcher above has no
# quoted tokens; its whitespace compaction must never normalize a portal name.
PORTAL_NAMED = re.compile(
    r"\s*function\s+enter\s*\(\s*pi\s*\)\s*\{\s*"
    r"pi\s*\.\s*warp\s*\(\s*(?P<map>0|[1-9]\d*)\s*,\s*"
    r"(?P<quote>[\"'])(?P<name>[A-Za-z0-9_]{1,64})(?P=quote)\s*\)\s*;?\s*"
    r"(?:return\s+true\s*;?\s*)?\}\s*"
)


def convert(raw: bytes, relative: str) -> tuple[str, dict] | None:
    text = raw.decode("utf-8-sig", errors="strict")
    compact = re.sub(r"\s+", "", without_comments(text))
    match = WRAPPER.fullmatch(compact)
    if not match:
        return None
    digest = hashlib.sha256(raw).hexdigest()
    shop = match.group("shop")
    command = "npc:open_storage()" if shop is None else f"npc:open_shop({int(shop)})"
    lua = (
        f"-- Source: Legacy scripts/{relative}\n"
        f"-- Source SHA256: {digest}\n"
        "-- Unconditional service wrapper; no legacy callback state remains.\n"
        f"{command}\n"
    )
    entry = {
        "legacy": relative,
        "source_sha256": digest,
        "lua": str(Path(relative).with_suffix(".lua")).replace("\\", "/"),
        "lua_sha256": hashlib.sha256(lua.encode()).hexdigest(),
        "service": "storage" if shop is None else "shop",
    }
    if shop is not None:
        entry["shop_id"] = int(shop)
    return lua, entry


def convert_portal(raw: bytes, relative: str) -> tuple[str, dict] | None:
    text = without_comments(raw.decode("utf-8-sig", errors="strict"))
    named = PORTAL_NAMED.fullmatch(text)
    if named:
        map_id = int(named.group("map"))
        if map_id > 999999998:
            return None
        portal_name = named.group("name")
        digest = hashlib.sha256(raw).hexdigest()
        lua = (f"-- Source: Legacy scripts/{relative}\n"
               f"-- Source SHA256: {digest}\n"
               "-- Unconditional named warp; host resolves the exact WZ portal name.\n"
               f'portal:warp({map_id}, "{portal_name}")\n')
        return lua, {"legacy": relative, "source_sha256": digest,
                     "lua": str(Path(relative).with_suffix(".lua")).replace("\\", "/"),
                     "lua_sha256": hashlib.sha256(lua.encode()).hexdigest(),
                     "map_id": map_id, "portal_name": portal_name}
    compact = re.sub(r"\s+", "", text)
    match = PORTAL.fullmatch(compact)
    if not match:
        return None
    map_id, portal_id = int(match.group("map")), int(match.group("portal") or "0")
    if map_id > 999999998 or portal_id > 255:
        return None
    digest = hashlib.sha256(raw).hexdigest()
    lua = (f"-- Source: Legacy scripts/{relative}\n"
           f"-- Source SHA256: {digest}\n"
           "-- Unconditional portal warp; host validates the source portal and destination.\n"
           f"portal:warp({map_id}, {portal_id})\n")
    return lua, {"legacy": relative, "source_sha256": digest,
                 "lua": str(Path(relative).with_suffix(".lua")).replace("\\", "/"),
                 "lua_sha256": hashlib.sha256(lua.encode()).hexdigest(),
                 "map_id": map_id, "portal_id": portal_id}


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source-root", required=True, type=Path)
    parser.add_argument("--write", action="store_true")
    parser.add_argument("--include-portals", action="store_true")
    args = parser.parse_args()
    source = args.source_root.resolve(strict=True)
    dest = Path(__file__).resolve().parent.parent / "scripts"
    entries = []
    for path in sorted((source / "npc").iterdir()):
        if not path.is_file() or path.suffix.lower() != ".js":
            continue
        raw = path.read_bytes()
        try:
            result = convert(raw, "npc/" + path.name)
        except (UnicodeDecodeError, ValueError):
            continue
        if result is None:
            continue
        lua, entry = result
        target = dest / entry["lua"]
        if target.exists() and target.read_bytes() != lua.encode():
            raise RuntimeError(f"refusing to replace separately reviewed script: {target}")
        entries.append(entry)
        if args.write:
            target.write_bytes(lua.encode())
    manifest = {"version": 1, "method": "exact-unconditional-service-wrapper", "scripts": entries}
    if args.write:
        (dest / "service-npcs.json").write_text(json.dumps(manifest, indent=2) + "\n", encoding="utf-8")
    portals = []
    if args.include_portals:
        for path in sorted((source / "portal").iterdir()):
            if not path.is_file() or path.suffix.lower() != ".js":
                continue
            try:
                result = convert_portal(path.read_bytes(), "portal/" + path.name)
            except (UnicodeDecodeError, ValueError):
                continue
            if result is None:
                continue
            lua, entry = result
            target = dest / entry["lua"]
            if target.exists() and target.read_bytes() != lua.encode():
                raise RuntimeError(f"refusing to replace separately reviewed script: {target}")
            portals.append(entry)
            if args.write:
                target.parent.mkdir(parents=True, exist_ok=True)
                target.write_bytes(lua.encode())
        if args.write:
            portal_manifest = {"version": 1, "method": "exact-unconditional-numeric-or-named-warp", "scripts": portals}
            (dest / "portal-migrations.json").write_text(json.dumps(portal_manifest, indent=2) + "\n", encoding="utf-8")
    print(json.dumps({"matched": len(entries), "written": args.write,
                      "storage": sum(x["service"] == "storage" for x in entries),
                      "shop": sum(x["service"] == "shop" for x in entries), "portals": len(portals)}))


if __name__ == "__main__":
    main()
