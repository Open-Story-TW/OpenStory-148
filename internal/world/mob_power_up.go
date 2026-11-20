package world

import (
	"time"

	"maple-go-server/internal/combat"
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

// The controller may animate only the power-up the world offered during its
// current field visit. The mob itself owns MP, timing and status application.
type mobPowerUpCast struct {
	spentMP        int
	availableAfter time.Time
	offerUntil     time.Time
	offerer        *gameplayMember
	action         content.MobSkillAction
	value          content.MobPowerUp
	effectAt       time.Time
}

func (m *gameplayMob) currentMP() int {
	return max(0, m.stats.MP-m.cast.spentMP)
}

// The native move ACK's aggro flag enables original controller mode 4.
// Recognize a real settled combat contribution in this controller generation.
func (m *gameplayMob) powerUpControllerEngaged(member *gameplayMember, characterID int64) bool {
	if len(m.stats.Skills) == 0 {
		return false
	}
	for _, c := range m.contributions {
		if c.Damage > 0 && c.CharacterID == characterID && c.SessionID == member.sessionID && c.Generation == member.generation {
			return true
		}
	}
	return false
}

func (m *gameplayMob) activeMobStatus(stat combat.MobStat, now time.Time) *combat.ActiveStatus {
	if value := m.statuses[stat]; value != nil && value.ExpiresAt.After(now) {
		return value
	}
	return nil
}

func (m *gameplayMob) canPowerUp(now time.Time) bool {
	return m.hp > 0 && !m.claiming && !now.Before(m.cast.availableAfter) &&
		m.activeMobStatus(combat.MobMagicCrash, now) == nil &&
		m.activeMobStatus(combat.MobPowerUp, now) == nil &&
		m.activeMobStatus(combat.MobStun, now) == nil &&
		m.activeMobStatus(combat.MobFreeze, now) == nil &&
		m.activeMobStatus(combat.MobSealSkill, now) == nil
}

func (m *gameplayMob) enoughForPowerUp(v content.MobPowerUp) bool {
	maxHP := m.stats.HP
	threshold := (maxHP/100)*int64(v.HPPercent) + (maxHP%100)*int64(v.HPPercent)/100
	return m.currentMP() >= v.MP && m.hp <= threshold
}

func (m *gameplayMob) powerUpFromMove(c *content.Catalog, member *gameplayMember, move protocol.MonsterMove, now time.Time) (content.MobSkillAction, bool) {
	cast := &m.cast
	if !m.canPowerUp(now) {
		cast.offerer = nil
		return content.MobSkillAction{}, false
	}
	if cast.offerer == member && now.Before(cast.offerUntil) {
		a := cast.action
		// WZ skill1 is action 22 on v148's outgoing movement report.
		// Its low bit is facing, not part of the action index.
		if move.Action>>1 == a.Action+21 && uint16(move.SkillData) == uint16(a.ID)|uint16(a.Level)<<8 &&
			m.enoughForPowerUp(cast.value) {
			cast.spentMP += cast.value.MP
			cast.availableAfter = now.Add(cast.value.Cooldown)
			cast.effectAt = now.Add(a.EffectAfter)
			cast.offerer = nil
			return content.MobSkillAction{}, true
		}
		return content.MobSkillAction{}, false
	}
	cast.offerer = nil
	if !move.UseSkill || move.SkillData != 0 {
		return content.MobSkillAction{}, false
	}
	for _, action := range m.stats.Skills {
		if value, ok := c.PowerUp(action.Level); ok && m.enoughForPowerUp(value) {
			cast.offerer, cast.offerUntil = member, now.Add(5*time.Second)
			cast.action, cast.value = action, value
			return action, false
		}
	}
	return content.MobSkillAction{}, false
}

func (m *gameplayMob) applyPowerUpLocked(now time.Time) []byte {
	cast := &m.cast
	if cast.effectAt.IsZero() || now.Before(cast.effectAt) || m.claiming {
		return nil
	}
	due := cast.effectAt
	cast.effectAt = time.Time{}
	if m.hp <= 0 || m.activeMobStatus(combat.MobMagicCrash, now) != nil {
		return nil
	}
	// The native MobSkill reason encodes skill and level as two U16 values.
	status := combat.ActiveStatus{Stat: combat.MobPowerUp, Value: int32(cast.value.Power),
		SkillID:   int32(cast.action.ID) | int32(cast.action.Level)<<16,
		ExpiresAt: due.Add(cast.value.Duration)}
	return applyMobStatusesLocked(m, []combat.ActiveStatus{status}, domain.CharacterSession{}, now)
}
