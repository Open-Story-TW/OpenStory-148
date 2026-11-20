package world

import (
	"context"
	"log/slog"
	"time"

	"maple-go-server/internal/combat"
	"maple-go-server/internal/domain"
)

// The v148 operator's SG-88 activation belongs to one native field visit.
const sceneLocalSG88 = 35121003

// dropRelogBuffs clears the skill buffs a fresh login carries while
// MAPLEGO_RELOG_BUFFS is off, before any field packet is written. Channel
// changes and cash shop returns never come here. A failure keeps the buffs
// and the login goes on.
func (s *Server) dropRelogBuffs(ctx context.Context, live *liveCharacter) {
	repo, ok := s.repo.(domain.SkillBuffClearer)
	if !ok {
		return
	}
	live.persistMu.Lock()
	defer live.persistMu.Unlock()
	live.mu.Lock()
	session := live.session
	live.mu.Unlock()
	if len(session.Character.SkillBuffs) == 0 {
		return
	}
	session.Lease = s.currentLease()
	clearCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	r, err := repo.ClearSkillBuffs(clearCtx, session)
	cancel()
	if err != nil {
		slog.Warn("relog_buffs_clear_failed", "character", session.CharacterID, "error", err)
		return
	}
	live.mu.Lock()
	c := &live.session.Character
	c.SkillBuffs = r.Character.SkillBuffs
	if r.Changed {
		c.HP, c.MP, c.Revision = r.Character.HP, r.Character.MP, r.Character.Revision
		c.MaxHP, c.MaxMP = r.Character.MaxHP, r.Character.MaxMP
		c.BaseMaxHP, c.BaseMaxMP, c.VitalsModel, c.VitalsOrigin = r.Character.BaseMaxHP, r.Character.BaseMaxMP, r.Character.VitalsModel, r.Character.VitalsOrigin
	}
	live.mu.Unlock()
}

// restoreSummons respawns the summon of each active buff row whose skill
// summons on cast, ending when the buff ends. Summons are field objects that
// do not survive leaving the field; they come back on every channel change and
// cash shop return, and on a login while MAPLEGO_RELOG_BUFFS keeps the buffs.
func (s *Server) restoreSummons(channel int, live *liveCharacter) {
	s.restoreSummonsMatching(channel, live, nil)
}

// followSummons brings an owner's moving summons along on a map change; the
// leave removed them from the old field. Stationary kinds (0 stationary,
// 2 walk-stationary, 5 circle-stationary) stay behind, as in the original.
func (s *Server) followSummons(channel int, live *liveCharacter) {
	s.restoreSummonsMatching(channel, live, movingSummon)
}

func movingSummon(spec *combat.SummonSpec) bool {
	switch spec.MoveAbility {
	case 0, 2, 5:
		return false
	}
	return !spec.Puppet
}

func (s *Server) restoreSummonsMatching(channel int, live *liveCharacter, keep func(*combat.SummonSpec) bool) {
	if s.gameplay == nil {
		return
	}
	live.mu.Lock()
	c := cloneCombatSession(live.session).Character
	live.mu.Unlock()
	now := time.Now()
	done := map[int]bool{}
	for _, b := range activeSkillBuffs(c, now) {
		if done[b.ID] || b.ID == sceneLocalSG88 {
			continue
		}
		done[b.ID] = true
		spec, ok := attackSpecFor(b.ID)
		if !ok || spec.Summon == nil || spec.Summon.NoCastSpawn || keep != nil && !keep(spec.Summon) {
			continue
		}
		if _, err := s.spawnSummonAt(channel, live, b.ID, b.Level, nil, now, b.ExpiresAt); err != nil {
			slog.Warn("relog_summon_restore_failed", "character", c.ID, "skill", b.ID, "error", err)
		}
	}
}
