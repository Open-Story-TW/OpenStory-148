"""Build internal/content/skills.json from Java Legacy wz/Skill.wz.

The seed is what SkillDefinition reads when MAPLEGO_WZ_ROOT is empty. Besides
the level values it keeps the lt/rb range (common or per level) and the info
node, which party buffs, area attacks and the attack pipeline need.

    py -I tools/build-skill-seed.py --java ../MapleStory_Legacy_Server
"""

import argparse
import json
import pathlib
import xml.etree.ElementTree as ET


def values(node):
    out = {}
    for x in node:
        if x.tag in ("int", "string") and x.get("name") != "hs":
            out[x.get("name")] = x.get("value")
    return out


def vectors(node):
    out = {}
    for x in node:
        if x.tag == "vector" and x.get("name") in ("lt", "rb"):
            out[x.get("name")] = [int(x.get("x")), int(x.get("y"))]
    return out


def child(node, name):
    if node is None:
        return None
    for x in node:
        if x.get("name") == name:
            return x
    return None


def skill(sid, node):
    def int_value(name, default=0):
        x = child(node, name)
        try:
            return int(x.get("value")) if x is not None else default
        except ValueError:
            return default

    s = {"id": sid, "master_level": int_value("masterLevel"), "req_level": int_value("reqLev"),
         "disabled": int_value("disable") != 0, "invisible": int_value("invisible") != 0,
         "passive": int_value("skillType") == 2, "requirements": {}}
    if int_value("combatOrders") > 0:
        s["combat_orders"] = True
    max_level = 0
    common = child(node, "common")
    level = child(node, "level")
    if common is not None:
        s["common"] = values(common)
        max_level = int(s["common"].get("maxLevel", "0") or 0)
        v = vectors(common)
        if v:
            s["range"] = v
    elif level is not None:
        s["levels"] = {}
        ranges = {}
        for lv in level:
            n = int(lv.get("name"))
            s["levels"][str(n)] = values(lv)
            max_level = max(max_level, n)
            v = vectors(lv)
            if v:
                ranges[str(n)] = v
        if ranges:
            s["level_ranges"] = ranges
    req = child(node, "req")
    if req is not None:
        for x in req:
            s["requirements"][x.get("name")] = int(x.get("value"))
    info = child(node, "info")
    if info is not None:
        i = {x.get("name"): int(x.get("value")) for x in info if x.tag == "int"}
        if i:
            s["info"] = i
    attack = child(child(child(node, "summon"), "attack1"), "info")
    if attack is not None:
        a = {x.get("name"): int(x.get("value")) for x in attack if x.tag == "int"}
        if a:
            s["summon_attack"] = a
    s["max_level"] = max_level
    return s


def main():
    p = argparse.ArgumentParser()
    p.add_argument("--java", required=True)
    p.add_argument("--output", default="internal/content/skills.json")
    a = p.parse_args()
    out = []
    for f in sorted((pathlib.Path(a.java) / "wz" / "Skill.wz").glob("*.img.xml")):
        stem = f.name.split(".")[0]
        if not stem.isdigit():
            continue
        root = child(ET.parse(f).getroot(), "skill")
        if root is None:
            continue
        for node in root:
            if node.get("name", "").isdigit():
                out.append(skill(int(node.get("name")), node))
    out.sort(key=lambda s: s["id"])
    with open(a.output, "w", encoding="utf-8", newline="\n") as fh:
        fh.write(json.dumps(out))


if __name__ == "__main__":
    main()
