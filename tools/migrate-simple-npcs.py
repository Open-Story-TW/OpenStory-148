"""Migrate whole-source literal dialogue and numeric NPC warp wrappers.

This recognizes a deliberately small token grammar, never executes JavaScript,
and refuses dynamic expressions, additional callbacks, conditions and host calls.
It is separate from the unconditional shop/storage/portal wrapper inventory.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import re
from pathlib import Path


def tokens(source: str) -> list[str | tuple[str, str]]:
    result: list[str | tuple[str, str]] = []
    i = 0
    while i < len(source):
        c = source[i]
        if c.isspace():
            i += 1
        elif source.startswith("//", i):
            end = source.find("\n", i + 2)
            i = len(source) if end < 0 else end
        elif source.startswith("/*", i):
            end = source.find("*/", i + 2)
            if end < 0:
                raise ValueError("unterminated comment")
            i = end + 2
        elif c in "\"'":
            quote, out = c, []
            i += 1
            while i < len(source) and source[i] != quote:
                c = source[i]
                if c in "\r\n":
                    raise ValueError("unescaped newline")
                if c != "\\":
                    out.append(c)
                    i += 1
                    continue
                i += 1
                if i >= len(source):
                    raise ValueError("unterminated escape")
                escape = source[i]
                if escape in "\"'\\":
                    out.append(escape)
                    i += 1
                elif escape in "bfnrtv":
                    out.append(dict(b="\b", f="\f", n="\n", r="\r", t="\t", v="\v")[escape])
                    i += 1
                elif escape in "xu":
                    size = 2 if escape == "x" else 4
                    value = source[i + 1:i + 1 + size]
                    if not re.fullmatch(r"[0-9a-fA-F]{%d}" % size, value):
                        raise ValueError("invalid Unicode escape")
                    out.append(chr(int(value, 16)))
                    i += size + 1
                else:
                    # Do not guess legacy octal, continuation or identity escapes.
                    raise ValueError("unsupported escape")
            if i == len(source):
                raise ValueError("unterminated string")
            i += 1
            text = "".join(out)
            # Decode paired JS UTF-16 surrogates; reject unpaired surrogates.
            text = text.encode("utf-16-le", "surrogatepass").decode("utf-16-le")
            result.append(("string", text))
        elif c.isascii() and (c.isalpha() or c in "_$"):
            match = re.match(r"[A-Za-z_$][A-Za-z0-9_$]*", source[i:])
            assert match is not None
            result.append(match[0])
            i += len(match[0])
        elif c.isascii() and c.isdigit():
            match = re.match(r"(?:0|[1-9][0-9]*)(?![0-9A-Za-z_])", source[i:])
            if match is None:
                raise ValueError("non-decimal numeric literal")
            result.append(("integer", match[0]))
            i += len(match[0])
        elif c in "(){}.,;=+-":
            result.append(c)
            i += 1
        else:
            raise ValueError("unsupported token")
    return result


def lua_string(text: str) -> str:
    escapes = {"\\": "\\\\", '"': '\\"', "\n": "\\n", "\r": "\\r", "\t": "\\t"}
    return '"' + "".join(escapes.get(c, f"\\{ord(c):03d}" if ord(c) < 32 or ord(c) == 127 else c) for c in text) + '"'


def convert(raw: bytes, relative: str) -> tuple[str, dict] | None:
    ts = tokens(raw.decode("utf-8-sig", errors="strict"))
    if len(ts) >= 6 and ts[:4] == ["var", "status", "=", "-"] and isinstance(ts[4], tuple) and ts[4][0] == "integer" and ts[5] == ";":
        ts = ts[6:]
    elif len(ts) >= 5 and ts[:3] == ["var", "status", "="] and isinstance(ts[3], tuple) and ts[3][0] == "integer" and ts[4] == ";":
        ts = ts[5:]
    initializer = ["function", "start", "(", ")", "{", "status", "=", "-", ("integer", "1"), ";", "action", "(", ("integer", "1"), ",", ("integer", "0"), ",", ("integer", "0"), ")", ";", "}"]
    had_initializer = ts[:len(initializer)] == initializer
    if had_initializer:
        ts = ts[len(initializer):]
    start = ["function", "start", "(", ")", "{"]
    action = ["function", "action", "(", "mode", ",", "type", ",", "selection", ")", "{"]
    if not had_initializer and ts[:len(start)] == start:
        body = ts[len(start):]
    elif ts[:len(action)] == action:
        body = ts[len(action):]
    else:
        return None
    suffix = ["cm", ".", "dispose", "(", ")", ";", "}"]
    if body[-len(suffix):] != suffix:
        return None
    body = body[:-len(suffix)]
    entry: dict = {"legacy": relative, "source_sha256": hashlib.sha256(raw).hexdigest(),
                   "lua": str(Path(relative).with_suffix(".lua")).replace("\\", "/")}
    if body[:4] == ["cm", ".", "sendOk", "("] and body[-2:] == [")", ";"]:
        expression = body[4:-2]
        if not expression or len(expression) % 2 == 0:
            return None
        if any(x != "+" for x in expression[1::2]) or any(not isinstance(x, tuple) or x[0] != "string" for x in expression[::2]):
            return None
        text = "".join(x[1] for x in expression[::2])
        if not text or len(text.encode()) > 4096 or "\x00" in text:
            return None
        entry.update(kind="dialogue", text=text)
        command = f"npc:say({lua_string(text)}, false, false)"
    elif body[:4] == ["cm", ".", "warp", "("] and body[-2:] == [")", ";"]:
        args = body[4:-2]
        if len(args) not in (1, 3) or not isinstance(args[0], tuple) or args[0][0] != "integer":
            return None
        if len(args) == 3 and (args[1] != "," or not isinstance(args[2], tuple) or args[2][0] != "integer"):
            return None
        map_id, portal_id = int(args[0][1]), int(args[2][1]) if len(args) == 3 else 0
        if map_id > 999999998 or portal_id > 255:
            return None
        entry.update(kind="warp", map_id=map_id, portal_id=portal_id)
        command = f"npc:warp({map_id}, {portal_id})"
    else:
        return None
    lua = (f"-- Source: Legacy scripts/{relative}\n-- Source SHA256: {entry['source_sha256']}\n"
           "-- Exact literal wrapper; no callback state or dynamic expression.\n" + command + "\n")
    entry["lua_sha256"] = hashlib.sha256(lua.encode()).hexdigest()
    return lua, entry


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source-root", type=Path, required=True)
    parser.add_argument("--write", action="store_true")
    args = parser.parse_args()
    source = args.source_root.resolve(strict=True)
    dest = Path(__file__).resolve().parent.parent / "scripts"
    entries = []
    for path in sorted((source / "npc").glob("*.js")):
        # Runtime NPC names are numeric. Non-numeric aliases need routing review.
        if not path.stem.isascii() or not path.stem.isdecimal():
            continue
        try:
            result = convert(path.read_bytes(), "npc/" + path.name)
        except (UnicodeError, ValueError):
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
    if args.write:
        (dest / "simple-npcs.json").write_text(json.dumps({"version": 1, "method": "whole-source-literal-wrapper", "scripts": entries}, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print(json.dumps({"matched": len(entries), "written": args.write,
                      "dialogue": sum(x["kind"] == "dialogue" for x in entries),
                      "warp": sum(x["kind"] == "warp" for x in entries)}))


if __name__ == "__main__":
    main()
