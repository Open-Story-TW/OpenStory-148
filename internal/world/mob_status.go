package world

import (
	"fmt"
	"os"
	"time"

	"maple-go-server/internal/combat"
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

// Monster statuses live with the room's mob under gameplay.mu. They are
// transient: respawn, kill and room teardown drop them. DoT never kills
// (combat.MobStatuses.Tick stops at 1 HP) so no settlement is needed; its
// damage is credited to the owner's contribution like a nonlethal swing.

func (m *gameplayMob) statusValues() []protocol.MobStatValue {
	if len(m.statuses) == 0 {
		return nil
	}
	return m.statuses.Values()
}

// spawnPacket is SpawnMonster with current temporary stats for late joiners.
func (m *gameplayMob) spawnPacket(controller bool, now time.Time) []byte {
	if m.fake {
		if controller {
			return nil
		}
		return protocol.SpawnMonsterProjection(m.objectID, m.life)
	}
	return protocol.SpawnMonsterWithStats(m.objectID, m.life, controller, m.statusValues(), now)
}

// applyMobStatusesLocked stores resolved statuses and returns the packet for
// room peers. Caller holds gameplay.mu and has checked the mob is alive.
func applyMobStatusesLocked(m *gameplayMob, add []combat.ActiveStatus, owner domain.CharacterSession, now time.Time) []byte {
	if m.hp <= 0 || len(add) == 0 {
		return nil
	}
	if m.statuses == nil {
		m.statuses = combat.MobStatuses{}
	}
	var applied []protocol.MobStatValue
	for _, st := range add {
		st.OwnerID, st.OwnerSession, st.OwnerGeneration = owner.CharacterID, owner.SessionID, owner.Generation
		if m.statuses.Apply(st, now) {
			st = *m.statuses[st.Stat]
			applied = append(applied, protocol.MobStatValue{Stat: st.Stat, Value: st.Value, Reason: st.SkillID, ExpiresAt: st.ExpiresAt})
		}
	}
	if len(applied) == 0 {
		return nil
	}
	return protocol.ApplyMonsterStatus(m.objectID, applied, 0, now)
}

// tickMobStatuses expires statuses and applies DoT for every occupied room.
func (g *gameplaySpace) tickMobStatuses(now time.Time) {
	type delivery struct {
		sink   packetSink
		packet []byte
	}
	var out []delivery
	g.mu.Lock()
	for _, r := range g.rooms {
		if len(r.members) == 0 {
			continue
		}
		for _, mob := range r.mobs {
			if len(mob.statuses) == 0 || mob.hp <= 0 {
				continue
			}
			if !mob.claiming {
				hp, hits := mob.statuses.Tick(now, mob.hp)
				for _, h := range hits {
					owner := domain.CharacterSession{CharacterID: h.OwnerID}
					for _, st := range mob.statuses {
						if st.OwnerID == h.OwnerID && st.SkillID == h.SkillID {
							owner.SessionID, owner.Generation = st.OwnerSession, st.OwnerGeneration
						}
					}
					mob.contributions = addCombatDamage(mob.contributions, owner, int(h.Damage))
				}
				if hp != mob.hp {
					mob.hp = hp
					for id, m := range r.members {
						for _, h := range hits {
							if h.OwnerID == id {
								out = append(out, delivery{m.sink, protocol.MonsterHP(mob.objectID, mob.hp, mob.stats.HP)})
								break
							}
						}
					}
				}
			}
			if ended := mob.statuses.Expire(now); len(ended) > 0 {
				p := protocol.CancelMonsterStatus(mob.objectID, ended)
				for _, m := range r.members {
					out = append(out, delivery{m.sink, p})
				}
			}
		}
	}
	g.mu.Unlock()
	for _, d := range out {
		if d.packet != nil {
			_ = d.sink.WritePacket(d.packet)
		}
	}
}

// CastOnMobs applies a registered monster-targeted cast (Threaten, Slow,
// Seal, Hypnotize, Dispel). S0a's cast path calls it with the monster object
// IDs from the cast packet; targets are bounded by mobCount and range.
func (s *Server) CastOnMobs(channel int, live *liveCharacter, skillID, level int, oids []int32, now time.Time) (int, error) {
	spec, ok := attackSpecFor(skillID)
	if !ok || spec.MobCast == nil || level < 1 || s.gameplay == nil {
		return 0, fmt.Errorf("skill%d has no registered monster cast", skillID)
	}
	effect := spec.Effect
	if effect.SkillID == 0 {
		template, err := content.SkillDefinition(os.Getenv("MAPLEGO_WZ_ROOT"), skillID)
		if err != nil {
			return 0, err
		}
		if effect, err = combat.FromSkill(template, level); err != nil {
			return 0, err
		}
	}
	live.mu.Lock()
	session := live.session
	live.mu.Unlock()
	c := session.Character
	stats, err := combat.AttackStats(c, now)
	if err != nil {
		return 0, err
	}
	if len(oids) > effect.MobCount && !spec.MobCast.WholeField {
		oids = oids[:effect.MobCount]
	}
	g := s.gameplay
	g.mu.Lock()
	room := g.rooms[mapKey{channel: channel, mapID: c.MapID}]
	if room == nil || room.members[c.ID] == nil || room.members[c.ID].live != live {
		g.mu.Unlock()
		return 0, fmt.Errorf("caster not in field")
	}
	var out []roomDelivery
	hit := 0
	if spec.MobCast.WholeField {
		oids = oids[:0]
		for oid := range room.mobs {
			oids = append(oids, oid)
		}
	}
	for _, oid := range oids {
		mob := room.mobs[oid]
		if mob == nil || mob.hp <= 0 || spec.MobCast.SkipBoss && mob.stats.Boss || !spec.MobCast.WholeField && !mobCastReaches(spec.MobCast, effect, c, mob) {
			continue
		}
		hit++
		if spec.MobCast.Dispel && (effect.Prop == 0 || rollProp(effect.Prop)) {
			var removed []combat.MobStat
			for _, st := range combat.DispelStats {
				if mob.statuses.Has(st) {
					delete(mob.statuses, st)
					removed = append(removed, st)
				}
			}
			if len(removed) > 0 {
				out = append(out, roomBroadcast(room, protocol.CancelMonsterStatus(mob.objectID, removed), -1)...)
			}
		}
		var add []combat.ActiveStatus
		// Java applyMonsterBuff rolls prop once per monster and then applies
		// every status (Threaten PAD+PDD+ACC together).
		landed := effect.Prop <= 0 || rollProp(effect.Prop)
		for _, ap := range spec.MobCast.Statuses {
			if !landed || !combat.CanInflict(mob.stats, ap.Stat, effect.Element) {
				continue
			}
			if st, ok := combat.ResolveOn(ap, effect, c.ID, combat.MaxBaseDamage(stats), mob.stats, now, alwaysRoll); ok {
				add = append(add, st)
			}
		}
		if p := applyMobStatusesLocked(mob, add, session, now); p != nil {
			out = append(out, roomBroadcast(room, p, -1)...)
		}
	}
	g.mu.Unlock()
	deliver(out)
	return hit, nil
}

func mobCastReaches(m *combat.MobCastSpec, e combat.Effect, c domain.Character, mob *gameplayMob) bool {
	p := combat.Point{X: mob.life.X, Y: mob.life.Y}
	if m.UseBox && e.Box.Set {
		// Native casts face either way; accept either mirrored box.
		lt, rb := e.Box.At(combat.Point{X: c.X, Y: c.Y}, true)
		lt2, rb2 := e.Box.At(combat.Point{X: c.X, Y: c.Y}, false)
		return combat.Inside(lt, rb, p) || combat.Inside(lt2, rb2, p)
	}
	r := e.Range
	if r <= 0 {
		r = 400
	}
	return within(c.X, c.Y, p.X, p.Y, r+60)
}

// alwaysRoll is the per-status roll once a cast's single prop roll landed.
func alwaysRoll(int) bool { return true }
