"""Build the complete skill acceptance inventory without inferring support from WZ."""
import argparse
import hashlib
import json
from pathlib import Path
import xml.etree.ElementTree as ET

SUPPORTED = {
    1000: ("attack", "implemented", ["internal/protocol/magic_attacks.go", "internal/world/magic_attacks.go", "internal/persistence/combat_settlement.go"], ["native_frontend", "actual_damage", "hit_lines", "target_limit", "range", "cost", "persistence"]),
    1001: ("buff", "implemented", ["internal/persistence/skills.go"], ["actual_heal", "cast_pixels", "late_foreign_entry", "foreign_reentry", "duration", "cooldown", "persistence"]),
    1002: ("buff", "implemented", ["internal/persistence/skills.go"], ["effective_speed", "cast_pixels", "late_foreign_entry", "foreign_reentry", "duration", "cooldown", "persistence"]),
    1000006: ("passive", "implemented", ["internal/content/vitals.go"], ["effective_max_hp", "equipment_recalculation", "persistence"]),
    1001003: ("buff", "implemented", ["internal/persistence/skills.go"], ["effective_defense", "cast_pixels", "late_foreign_entry", "foreign_reentry", "duration", "cancel", "persistence"]),
    1001004: ("attack", "implemented", ["internal/content/physical_skills.go", "internal/world/physical_attacks.go"], ["actual_damage", "hit_lines", "target_limit", "range", "cost", "persistence"]),
    1001005: ("attack", "implemented", ["internal/content/physical_skills.go", "internal/world/physical_attacks.go"], ["actual_damage_per_target", "hit_lines_per_target", "target_limit", "range", "cost", "persistence"]),
    2000006: ("passive", "implemented", ["internal/content/vitals.go", "internal/persistence/skills.go"], ["effective_max_mp", "equipment_recalculation", "no_automatic_heal", "persistence"]),
}


def build(seed_path: Path, names_path: Path | None) -> dict:
    raw = seed_path.read_bytes()
    skills = json.loads(raw.decode("utf-8-sig"))
    names = {}
    if names_path:
        for node in ET.parse(names_path).getroot():
            try:
                skill_id = int(node.attrib.get("name", ""))
            except ValueError:
                continue
            for child in node:
                if child.attrib.get("name") == "name":
                    names[skill_id] = child.attrib.get("value", "")
    ids = [skill["id"] for skill in skills]
    if len(ids) != len(set(ids)) or not set(SUPPORTED).issubset(ids):
        raise ValueError("duplicate or missing known skill IDs")
    rows = []
    for skill in sorted(skills, key=lambda row: row["id"]):
        skill_id = skill["id"]
        kind, support, source, required = SUPPORTED.get(skill_id, ("unclassified", "not_implemented_or_not_audited", [], ["classify_java_behavior", "implement_effect", "native_positive", "native_rejection", "persistence"]))
        rows.append({
            "skill_id": skill_id,
            "name": names.get(skill_id),
            "owner_job": skill_id // 10000,
            "max_level": skill.get("max_level", 0),
            "wz_disabled": skill.get("disabled", False),
            "wz_invisible": skill.get("invisible", False),
            "wz_passive_flag": skill.get("passive", False),
            "kind": kind,
            "go_effect_support": support,
            "go_sources": source,
            "native_acceptance": "pending_complete_user_criteria",
            "required_evidence": required,
            "evidence": [],
        })
    return {
        "version": 1,
        "seed": seed_path.as_posix(),
        "seed_sha256": hashlib.sha256(raw).hexdigest(),
        "skill_count": len(rows),
        "policy": "WZ data and learning support do not establish an implemented effect. This reviewed effect allowlist is intentionally explicit; update it only after source review. No skill is fully MCP-accepted until every applicable user criterion has evidence.",
        "mandatory_party_buff_cases": ["recipient_effective_stat_gain", "recipient_visual_effect", "out_of_range_or_wrong_map_rejected", "source_active_before_peer_enters_map", "peer_exit_and_reentry_without_crash", "foreign_mask_order_payload_values"],
        "skills": rows,
    }


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--seed", type=Path, default=Path("internal/content/skills.json"))
    parser.add_argument("--names", type=Path)
    parser.add_argument("--output", type=Path, default=Path("../open-maple/docs/server/baseline/skill-acceptance-inventory.json"))
    args = parser.parse_args()
    result = build(args.seed, args.names)
    args.output.write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print(json.dumps({"skill_count": result["skill_count"], "explicit_effects": len(SUPPORTED), "fully_mcp_accepted": 0}))


if __name__ == "__main__":
    main()
