package combat

import "maple-go-server/internal/content"

// instantKillSkills are attacks whose first line the v148 client replaces
// with the target's maximum HP when the skill's prop roll succeeds and the
// target is not a boss (damage calculation 0x71160B, maximum HP getter
// 0x5F736D). The client writes that value without the per-line display cap.
var instantKillSkills = map[int]bool{
	23110006: true, // 騰空踢擊 Aerial Barrage: 目標以 prop% 機率秒殺
}

// InstantKillLine reports whether line of skillID against mob is the
// client's instant-kill value: the first line, a non-boss target and exactly
// the template maximum HP.
func InstantKillLine(skillID, line int, mob content.Mob, claimed int64) bool {
	return instantKillSkills[skillID] && line == 0 && !mob.Boss && !mob.Invincible &&
		mob.HP > 0 && claimed == mob.HP
}
