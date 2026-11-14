"""Classify every v145 skill by mechanism using WZ + the Java Legacy source.

Output is a planning inventory, not acceptance evidence.
"""
import collections
import json
import re
import sys
import xml.etree.ElementTree as ET
from pathlib import Path

import argparse

ap = argparse.ArgumentParser(description=__doc__)
ap.add_argument("--java", type=Path, default=Path("../MapleStory_Legacy_Server"), help="Java Legacy checkout (src + wz XML)")
ap.add_argument("--go", type=Path, default=Path("."), help="Go checkout whose internal/ is scanned")
ap.add_argument("--staged", default="", help="';'-separated extra Go trees holding unmerged WIP")
ap.add_argument("--output", type=Path, default=Path("internal/skills/testdata/skill-plan-inventory.json"))
args = ap.parse_args()
JAVA, GO, OUT = args.java, args.go, args.output
STAGED = [Path(p) for p in args.staged.split(";") if p]

WZ = JAVA / "wz"
SRC = JAVA / "src"

# ---------- WZ ----------
names = {}
for node in ET.parse(WZ / "String.wz" / "Skill.img.xml").getroot():
    try:
        sid = int(node.get("name", ""))
    except ValueError:
        continue
    for c in node:
        if c.get("name") == "name":
            names[sid] = c.get("value", "")

skills = {}
for f in sorted((WZ / "Skill.wz").glob("*.img.xml")):
    stem = f.name.split(".")[0]
    if not stem.isdigit():
        continue
    root = ET.parse(f).getroot()
    sk = root.find("imgdir[@name='skill']")
    if sk is None:
        continue
    for s in sk:
        try:
            sid = int(s.get("name"))
        except (TypeError, ValueError):
            continue
        keys = set()
        common = s.find("imgdir[@name='common']")
        lvl = s.find("imgdir[@name='level']")
        src = common if common is not None else (lvl[0] if lvl is not None and len(lvl) else None)
        maxlv = 0
        if common is not None:
            for c in common:
                keys.add(c.get("name"))
                if c.get("name") == "maxLevel":
                    maxlv = int(c.get("value", "0"))
        elif lvl is not None:
            for l in lvl:
                for c in l:
                    keys.add(c.get("name"))
            maxlv = len(lvl)
        info = s.find("imgdir[@name='info']")
        info_type = None
        if info is not None:
            t = info.find("int[@name='type']")
            if t is not None:
                info_type = int(t.get("value"))
        def iv(name):
            n = s.find(f"int[@name='{name}']")
            return int(n.get("value")) if n is not None else 0
        skills[sid] = {
            "id": sid,
            "job": sid // 10000,
            "name": names.get(sid, ""),
            "max_level": maxlv,
            "keys": sorted(k for k in keys if k),
            "summon": s.find("imgdir[@name='summon']") is not None,
            "area": "lt" in keys or (src is not None and src.find("vector[@name='lt']") is not None),
            "psd": iv("psd") == 1,
            "invisible": iv("invisible") == 1,
            "disabled": iv("disable") == 1,
            "info_type": info_type,
            "elem": (s.find("string[@name='elemAttr']").get("value") if s.find("string[@name='elemAttr']") is not None else ""),
        }

# ---------- Java ----------
mse = (SRC / "server" / "MapleStatEffect.java").read_text(encoding="utf-8", errors="replace")
lines = mse.splitlines()

java_buff = collections.defaultdict(set)
java_mob = collections.defaultdict(set)
pending, in_case = [], False
for ln in lines:
    for m in re.finditer(r"case\s+(\d{3,9})\s*:", ln):
        if not in_case:
            pending = []
        pending.append(int(m.group(1)))
        in_case = True
    body = re.sub(r"case\s+\d+\s*:", "", re.sub(r"//.*", "", ln))
    if pending and body.strip():
        in_case = False
        for b in re.findall(r"MapleBuffStatus\.([A-Z_0-9]+)", body):
            for sid in pending:
                java_buff[sid].add(b)
        for b in re.findall(r"MonsterStatus\.([A-Z_0-9]+)", body):
            for sid in pending:
                java_mob[sid].add(b)
        if "break;" in body or "return" in body:
            pending = []

def ids_in_method(name):
    m = re.search(r"(?:boolean|int)\s+" + name + r"\s*\(\)\s*\{", mse)
    if not m:
        return set()
    depth, i = 0, m.end() - 1
    start = i
    while i < len(mse):
        if mse[i] == "{":
            depth += 1
        elif mse[i] == "}":
            depth -= 1
            if depth == 0:
                break
        i += 1
    return {int(x) for x in re.findall(r"\b(\d{4,9})\b", mse[start:i])}

party_excl = ids_in_method("isPartyBuff")
special = {k: ids_in_method(k) for k in ["isHeal", "isResurrection", "isMist", "isDispel", "isHeroWill", "isMagicDoor", "isMechDoor", "isComboRecharge", "isBeholder", "isMorph", "isMonsterRiding_", "isInfinity", "isDragonBlood", "isBerserk", "isMistEruption", "isSoaring", "isCharge", "isEnergyCharge"]}

# literal references across all Java src
refs = collections.defaultdict(set)
for f in SRC.rglob("*.java"):
    t = f.read_text(encoding="utf-8", errors="replace")
    rel = f.relative_to(SRC).as_posix()
    for m in set(re.findall(r"\b(\d{6,8})\b", t)):
        v = int(m)
        if v in skills:
            refs[v].add(rel)

# ---------- Go ----------
def go_refs(tree):
    out = collections.defaultdict(set)
    for f in (tree / "internal").rglob("*.go"):
        if f.name.endswith("_test.go"):
            continue
        t = f.read_text(encoding="utf-8", errors="replace")
        for m in set(re.findall(r"\b(\d{4,8})\b", t)):
            v = int(m)
            if v in skills and v >= 1000:
                out[v].add(f.relative_to(tree).as_posix())
    return out
go_main = go_refs(GO)
go_staged = collections.defaultdict(set)
for t in STAGED:
    for k, v in go_refs(t).items():
        go_staged[k] |= {f"{t.name}:{x}" for x in v}

GO_DONE = {1000, 1001, 1002, 1000006, 1001003, 1001004, 1001005, 2000006}

# ---------- classification ----------
MAGE = {2, 22, 12, 32, 27}
def branch(job):
    j = job
    if j in (0, 1000, 2000, 2001, 2002, 2003, 3000, 3001):
        return "beginner"
    if j < 1000:
        return {1: "warrior", 2: "magician", 3: "archer", 4: "thief", 5: "pirate", 8: "gm", 9: "gm"}.get(j // 100, "other")
    if j in (8000, 9000) or j >= 9100:
        return "common"
    if 1100 <= j < 1600:
        return "cygnus"
    if 2100 <= j < 2500:
        return "hero"
    if 3100 <= j < 3600:
        return "resistance"
    return "other"

def attack_kind(s):
    j = s["job"]
    fam = j // 100 if j < 1000 else (j // 100) % 10 if 1000 <= j < 2000 else None
    if (j < 1000 and j // 100 == 2) or (1200 <= j < 1300) or (2200 <= j < 2300) or (3200 <= j < 3300):
        return "magic"
    if (j < 1000 and j // 100 in (3,)) or 1300 <= j < 1400 or 2300 <= j < 2400 or 3300 <= j < 3400 or 3500 <= j < 3600:
        return "ranged"
    return "melee_or_ranged"

GROUPS = [
    ("W", lambda j: 100 <= j <= 132),
    ("M", lambda j: 200 <= j <= 232),
    ("B", lambda j: 300 <= j <= 322),
    ("T", lambda j: 400 <= j <= 434),
    ("P", lambda j: 500 <= j <= 532),
    ("C", lambda j: 1100 <= j <= 1511),
    ("AE", lambda j: 2100 <= j <= 2218),
    ("MP", lambda j: 2300 <= j <= 2412),
    ("R1", lambda j: 3100 <= j <= 3212),
    ("R2", lambda j: 3300 <= j <= 3512),
]
def group(job):
    for k, f in GROUPS:
        if f(job):
            return k
    return "Z"

rows = []
for sid, s in sorted(skills.items()):
    k = set(s["keys"])
    bstats = sorted(java_buff.get(sid, set()))
    mstats = sorted(java_mob.get(sid, set()))
    tags = []
    has_time = "time" in k
    attack = bool(k & {"damage", "mobCount", "attackCount", "fixdamage", "bulletCount"}) and not s["psd"]
    generic_buff = has_time and bool(k & {"pad", "pdd", "mad", "mdd", "acc", "eva", "speed", "jump", "mhpR", "mmpR", "indiePad", "indieMad", "indieMhp", "indieMmp", "indieSpeed", "indieJump", "indieAcc", "indieEva", "indieAllStat", "epad", "epdd", "emdd", "emhp", "emmp", "str", "dex", "int", "luk", "expR", "morph"})
    if s["psd"] or (not has_time and not attack and "mpCon" not in k and (k & {"mastery", "cr", "criticaldamageMin", "mhpR", "mmpR", "strX", "dexX", "intX", "lukX", "padX", "madX", "psdSpeed", "psdJump", "ar", "er", "asrR", "terR", "damR", "pddR", "mddR", "accX", "pddX", "mddX", "x", "y"})):
        tags.append("passive")
    if s["summon"]:
        tags.append("summon")
    if sid in special["isMist"]:
        tags.append("mist")
    if sid in special["isHeal"] or sid in special["isResurrection"] or sid in special["isDispel"]:
        tags.append("heal_dispel_res")
    if sid in special["isMagicDoor"] or sid in special["isMechDoor"]:
        tags.append("door")
    if sid in special["isMorph"] or "morph" in k:
        tags.append("morph")
    if sid in special["isMonsterRiding_"]:
        tags.append("mount")
    if mstats or ("prop" in k and attack and has_time):
        tags.append("mob_status")
    if "dot" in k:
        tags.append("dot")
    if attack and not s["summon"] and sid not in special["isMist"]:
        tags.append("attack_" + attack_kind(s))
    if (bstats or generic_buff or (has_time and not attack)) and "summon" not in tags and "passive" not in tags:
        tags.append("buff")
    if "buff" in tags and s["area"] and sid not in party_excl:
        tags.append("party")
    if s["info_type"] is not None and s["info_type"] in (34, 36):  # charge / keydown style
        tags.append("keydown_or_special")
    if not tags:
        tags.append("unclassified")
    # multiplayer interaction needed for acceptance
    multi = bool({"party", "summon", "mist", "heal_dispel_res", "door", "mob_status"} & set(tags))
    status = "go_implemented" if sid in GO_DONE else ("go_staged_wip" if sid in go_staged and sid not in go_main else ("go_referenced" if sid in go_main else "missing"))
    rows.append({
        "id": sid, "job": s["job"], "thread": group(s["job"]), "branch": branch(s["job"]), "name": s["name"], "max_level": s["max_level"],
        "tags": tags, "multiplayer": multi, "java_buffstats": bstats, "java_mobstatus": mstats,
        "java_files": sorted(refs.get(sid, set())), "go_status": status,
        "go_files": sorted(go_main.get(sid, set())), "go_staged": sorted(go_staged.get(sid, set())),
        "wz_disabled": s["disabled"], "wz_invisible": s["invisible"],
    })

body = ",\n".join(json.dumps(r, ensure_ascii=False, separators=(",", ":")) for r in rows)
POLICY = "Planning inventory only. Tags are heuristics from WZ keys and Java MapleStatEffect; they are not effect support or MCP acceptance."
OUT.write_text('{"version":1,"policy":%s,"skill_count":%d,"skills":[\n%s\n]}\n' % (json.dumps(POLICY), len(rows), body), encoding="utf-8")

# summary to stdout
by_job = collections.defaultdict(collections.Counter)
for r in rows:
    if r["wz_disabled"]:
        by_job[r["job"]]["disabled"] += 1
        continue
    by_job[r["job"]]["total"] += 1
    for t in r["tags"]:
        by_job[r["job"]][t.split("_")[0] if t.startswith("attack") else t] += 1
    if r["multiplayer"]:
        by_job[r["job"]]["MULTI"] += 1
    by_job[r["job"]][r["go_status"]] += 1
for j in sorted(by_job):
    print(j, dict(by_job[j]))
print("party_excl", sorted(party_excl))
print({k: sorted(v) for k, v in special.items() if v})
