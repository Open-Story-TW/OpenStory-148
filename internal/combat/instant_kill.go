package combat

import "maple-go-server/internal/content"

// instantKillSkills are attacks whose first line the v148 client replaces
// with the target's maximum HP when the skill's prop roll succeeds and the
// target is not a boss (damage calculation 0x71160B, maximum HP getter
// 0x5F736D). The client writes that value without the per-line display cap.
var instantKillSkills = map[int]bool{
	23110006: true, // 騰空踢擊 Aerial Barrage: 目標以 prop% 機率秒殺
}

// NativeMagicMelee mirrors the v148 melee sender's choice of damage routine
// (0x91D315 calls sub_6AEFB0): a skill of a magician-branch job (job%1000/100
// == 2, e.g. Battle Mage 32xx) or a stolen magician skill of a Phantom is
// scored by the magic routine 0x7073A6 even though it is sent as 0x55.
func NativeMagicMelee(job, skill int) bool {
	if skill == 0 {
		return false
	}
	return job%1000/100 == 2 || (job/100 == 24 || job == 2003) && skill/1000000 == 2
}

// capSentinelSkills are the Head Shots. Against a non-boss the v148 damage
// calculation 0x71160B sends a single line (0x711F18 sets the line count to
// 1) and fills it at 0x71219A..0x7121DD with the per-line cap minus a random
// [0, 10000) (sub_6F1BD3(rand, 10000.0, 0.0) subtracted from the cap at
// 0x7121CF). The cap is the native 999999 (0xC11080) or, once 0x610 armed
// it, the long-damage cap the client patches into that read.
var capSentinelSkills = map[int]bool{
	3221007: true, // 必殺狙擊
	5221016: true, // 爆頭射擊 Head Shot
	5721006: true, // 龍魂一擊
}

// capSentinelSpread is the random part the client subtracts from the cap.
const capSentinelSpread = 10000

// InstantKillLine reports whether line of skillID against mob is the
// client's instant-kill value: the first line against a non-boss target that
// is exactly the template maximum HP (Aerial Barrage) or the per-line cap
// minus less than 10000 (the Head Shots).
func InstantKillLine(skillID, line int, mob content.Mob, claimed int64) bool {
	if line != 0 || mob.Boss || mob.Invincible || mob.HP <= 0 {
		return false
	}
	if capSentinelSkills[skillID] {
		limit := int64(MaxLineDamage())
		return claimed <= limit && claimed > limit-capSentinelSpread
	}
	return instantKillSkills[skillID] && claimed == mob.HP
}
