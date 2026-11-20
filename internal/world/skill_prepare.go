package world

import (
	"context"
	"log/slog"
	"os"
	"time"

	"maple-go-server/internal/combat"
	"maple-go-server/internal/content"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/skills"
)

// skillPrepare handles 0x97 (Java PlayerHandler.SkillEffect): a learned
// keydown skill at its effective level starts charging; the room sees the
// charge animation and the key-down time is kept for the release attack.
func (s *Server) skillPrepare(channel int, live *liveCharacter, body []byte) {
	p, err := protocol.ReadSkillPrepare(protocol.NewReader(body))
	live.mu.Lock()
	c := live.session.Character
	live.mu.Unlock()
	if err != nil {
		slog.Warn("skill_prepare_rejected", "character", c.ID, "reason", "parse", "hex", hexBody(body))
		return
	}
	level := skills.LinkedLevel(c, levelSource(p.SkillID, nil), p.SkillID, time.Now())
	if c.HP <= 0 || level < 1 || int(p.Level) != level {
		slog.Warn("skill_prepare_rejected", "character", c.ID, "skill", p.SkillID, "level", p.Level, "learned", level)
		return
	}
	g := s.gameplay
	if g == nil {
		return
	}
	g.mu.Lock()
	room := g.rooms[mapKey{channel: channel, mapID: c.MapID}]
	if room == nil || room.members[c.ID] == nil || room.members[c.ID].live != live {
		g.mu.Unlock()
		return
	}
	m := room.members[c.ID]
	m.keydownSkill, m.keydownAt = p.SkillID, time.Now()
	out := roomBroadcast(room, protocol.SkillPreparePacket(c.ID, p), c.ID)
	g.mu.Unlock()
	deliver(out)
}

// endSkillPrepare broadcasts the end of a held skill (Java CANCEL_SKILL_EFFECT)
// when the client cancels the skill it is charging.
func (s *Server) endSkillPrepare(channel int, live *liveCharacter, skillID int) {
	g := s.gameplay
	if g == nil {
		return
	}
	live.mu.Lock()
	c := live.session.Character
	live.mu.Unlock()
	g.mu.Lock()
	room := g.rooms[mapKey{channel: channel, mapID: c.MapID}]
	var out []roomDelivery
	if room != nil {
		if m := room.members[c.ID]; m != nil && m.live == live && m.keydownSkill == skillID && skillID != 0 {
			m.keydownSkill = 0
			out = roomBroadcast(room, protocol.SkillPrepareCancel(c.ID, skillID), c.ID)
		}
	}
	g.mu.Unlock()
	deliver(out)
}

func hexBody(b []byte) string {
	const hexdigits = "0123456789abcdef"
	out := make([]byte, 0, 2*min(len(b), 64))
	for _, v := range b[:min(len(b), 64)] {
		out = append(out, hexdigits[v>>4], hexdigits[v&15])
	}
	return string(out)
}

func liveChannel(live *liveCharacter) int {
	live.mu.Lock()
	defer live.mu.Unlock()
	return live.session.ChannelID
}

// throwGrenade handles 0xAB (Java SpecialMove 4341003: keydown cancel, then
// applyTo -> applyMonsterBuff): the held skill ends for the room and its
// registered on-hit statuses land on monsters inside the WZ box at the
// grenade origin (either mirror), up to mobCount.
func (s *Server) throwGrenade(ctx context.Context, channel int, live *liveCharacter, sink packetSink, body []byte) {
	q, err := protocol.ReadThrowGrenade(protocol.NewReader(body))
	live.mu.Lock()
	session := live.session
	live.mu.Unlock()
	c := session.Character
	if err != nil {
		slog.Warn("grenade_rejected", "character", c.ID, "reason", "parse", "hex", hexBody(body))
		return
	}
	if q.SkillID == 5301001 {
		// The reviewed native release is exactly 29 bytes. Other grenade
		// families retain their existing path until independently reviewed.
		if len(body) != 29 || body[20] > 1 {
			return
		}
		_ = s.releaseBarrel(ctx, channel, live, sink, q, body)
		return
	}
	s.endSkillPrepare(channel, live, q.SkillID)
	if grenadeAttackFollows[q.SkillID] {
		// The v148 client follows this throw with its own attack packet,
		// which pays the cost, starts the cooldown and lands the status.
		// Settling the throw too made that attack fail on the cooldown.
		slog.Info("grenade_deferred_to_attack", "character", c.ID, "skill", q.SkillID)
		return
	}
	now := time.Now()
	spec, ok := attackSpecFor(q.SkillID)
	level := skills.LinkedLevel(c, levelSource(q.SkillID, spec), q.SkillID, now)
	if !ok || c.HP <= 0 || level < 1 || !within(c.X, c.Y, int(q.X), int(q.Y), 600) {
		slog.Warn("grenade_rejected", "character", c.ID, "skill", q.SkillID, "learned", level, "x", q.X, "y", q.Y, "server_x", c.X, "server_y", c.Y, "hex", hexBody(body))
		return
	}
	effect := spec.Effect
	if effect.SkillID == 0 {
		template, err := content.SkillDefinition(os.Getenv("MAPLEGO_WZ_ROOT"), q.SkillID)
		if err != nil {
			return
		}
		if effect, err = combat.FromSkill(template, level); err != nil {
			return
		}
	}
	applies := append(append([]combat.MobStatusApply(nil), effect.MonsterStatus...), spec.OnHit...)
	stats, err := combat.AttackStats(c, now)
	if err != nil || len(applies) == 0 || s.gameplay == nil {
		return
	}
	// q.X/q.Y is the grenade start (user ±30, -160, 0x86E2F7); like Java
	// applyMonsterBuff the WZ box is taken around the user.
	origin := combat.Point{X: c.X, Y: c.Y}
	inside := func(p combat.Point) bool {
		if !effect.Box.Set {
			return within(origin.X, origin.Y, p.X, p.Y, max(effect.Range, 150))
		}
		for _, left := range []bool{q.FacingLeft, !q.FacingLeft} {
			lt, rb := effect.Box.At(origin, left)
			if combat.Inside(combat.Point{X: lt.X - 30, Y: lt.Y - 30}, combat.Point{X: rb.X + 30, Y: rb.Y + 30}, p) {
				return true
			}
		}
		return false
	}
	apply := func() {
		g := s.gameplay
		g.mu.Lock()
		room := g.rooms[mapKey{channel: channel, mapID: c.MapID}]
		if room == nil || room.members[c.ID] == nil || room.members[c.ID].live != live {
			g.mu.Unlock()
			return
		}
		var out []roomDelivery
		hit := 0
		for _, mob := range room.mobs {
			if hit >= effect.MobCount || mob.hp <= 0 || mob.claiming || !inside(combat.Point{X: mob.life.X, Y: mob.life.Y}) {
				continue
			}
			hit++
			var add []combat.ActiveStatus
			for _, ap := range applies {
				if !combat.CanInflict(mob.stats, ap.Stat, effect.Element) {
					continue
				}
				if st, ok := combat.ResolveOn(ap, effect, c.ID, combat.MaxBaseDamage(stats), mob.stats, now, rollProp); ok {
					add = append(add, st)
				}
			}
			if p := applyMobStatusesLocked(mob, add, session, now); p != nil {
				out = append(out, roomBroadcast(room, p, -1)...)
			}
		}
		g.mu.Unlock()
		deliver(out)
		slog.Info("grenade_applied", "character", c.ID, "skill", q.SkillID, "x", q.X, "y", q.Y, "hold_ms", q.HoldMS, "mobs", hit)
	}
	// The release pays mpCon and starts the cooldown like Java SpecialMove:
	// a target-less CombatSkill settlement; the bomb lands only once paid.
	a := protocol.Attack{Opcode: protocol.RecvMeleeAttack, SkillID: q.SkillID, TByte: 1, Tick: uint32(now.UnixMilli()), X: int16(c.X), Y: int16(c.Y)}
	if q.FacingLeft {
		a.Display = 0x8000
	}
	if attackOnCooldown(c, q.SkillID, now) {
		slog.Warn("grenade_rejected", "character", c.ID, "skill", q.SkillID, "reason", "cooldown")
		return
	}
	_ = s.genericAttackGameplayThen(ctx, channel, live, sink, a, apply)
}

// grenadeAttackFollows are thrown skills whose damage and status arrive in a
// separate attack packet after the throw (Flashbang 4321002: 0xAB, then 0x55).
var grenadeAttackFollows = map[int]bool{4321002: true}
