package world

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"maple-go-server/internal/combat"
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/skills"
)

// genericAttack is the S0b registered-skill pipeline state on a ticket.
type genericAttack struct {
	attack     protocol.Attack
	level      int
	bulletItem int32
	statuses   [][]combat.ActiveStatus // Per request target, applied after commit.
	statusOut  [][]byte                // Apply-status packets built at commit.
	extraOut   [][]byte                // Room packets after the attack (summon removal).
	erupt      []int                   // Own area skills detonated when the attack lands.
	eruptAreas []*roomArea             // Explicit native2121003 objects, pinned to this ticket.
	// serverDamage marks damage the server computed (counter-attacks): no
	// user attack packet, only damage/HP/kill publication.
	serverDamage bool
	spec         *combat.AttackSpec
	clipped      int // Lines the client claimed above the server bound.
	belowFloor   int // Non-zero lines under the mastery floor.
	// then runs after the attack is applied and published (paid), e.g. the
	// Monster Bomb statuses of a ThrowGrenade release.
	then func()
	// Summon attacks publish 0x185 from the summon instead of a user attack.
	summon           *roomSummon
	summonAction     byte
	summonOwnerLevel int
}

// Critical history stays separate when a boss has a different rate.
type targetCriticalAudits struct {
	ordinary combat.CriticalAudit
	boss     combat.CriticalAudit
}

func (a *targetCriticalAudits) forTarget(boss bool, bossBonus int) *combat.CriticalAudit {
	if boss && bossBonus != 0 {
		return &a.boss
	}
	return &a.ordinary
}

var criticalAudits targetCriticalAudits

// legacyAttack reports families with reviewed bespoke handlers and MCP
// evidence; they keep their own path until migrated.
func legacyAttack(op uint16, job, skill int) bool {
	switch op {
	case protocol.RecvMeleeAttack:
		return job == 0 && skill == 0 // Warriors take the generic path: its bound counts passives and criticals.
	case protocol.RecvMagicAttack:
		return skill == 1000
	}
	return false
}

func attackSkillLevel(c domain.Character, id int, now time.Time) int {
	for _, s := range c.Skills {
		if s.ID == id && (s.ExpiresAt.IsZero() || s.ExpiresAt.After(now)) {
			return s.Level
		}
	}
	return 0
}

func attackOnCooldown(c domain.Character, id int, now time.Time) bool {
	for _, cd := range c.SkillCooldowns {
		if cd.ID == id && cd.ExpiresAt.After(now) {
			return true
		}
	}
	return false
}

// resolveAttackEffect returns the spec and effect for an attack, or false for
// unregistered/unlearned skills.
func resolveAttackEffect(c domain.Character, a *protocol.Attack, now time.Time) (*combat.AttackSpec, combat.Effect, int, bool) {
	if a.SkillID == 0 {
		if combat.KindForOpcode(a.Opcode) == combat.Magic {
			return nil, combat.Effect{}, 0, false
		}
		return combat.NormalAttack(), combat.NormalEffectFor(c), 0, true
	}
	spec, ok := attackSpecFor(a.SkillID)
	if !ok {
		return nil, combat.Effect{}, 0, false
	}
	if spec.Remap != nil {
		if to := spec.Remap(c, now); to != 0 && to != a.SkillID {
			if spec, ok = attackSpecFor(to); !ok {
				return nil, combat.Effect{}, 0, false
			}
			a.SkillID = to
		}
	}
	level := skills.LinkedLevel(c, levelSource(a.SkillID, spec), a.SkillID, now)
	if level < 1 || attackOnCooldown(c, a.SkillID, now) && !attackOngoing(c, *a, now) {
		return nil, combat.Effect{}, 0, false
	}
	if spec.Effect.SkillID != 0 {
		e := spec.Effect
		e.Level = level
		return spec, e, level, true
	}
	template, err := content.SkillDefinition(os.Getenv("MAPLEGO_WZ_ROOT"), a.SkillID)
	if err != nil {
		return nil, combat.Effect{}, 0, false
	}
	if level > template.TrueMax() {
		return nil, combat.Effect{}, 0, false
	}
	e, err := combat.FromSkill(template, level)
	if err != nil {
		return nil, combat.Effect{}, 0, false
	}
	return spec, e, level, true
}

// levelSource is the learned skill authorizing id: AttackSpec.LevelFrom, then
// S0a's Handler.LevelFrom (linked casts such as 35121011 <- 35121009).
func levelSource(id int, spec *combat.AttackSpec) int {
	if spec != nil && spec.LevelFrom != 0 {
		return spec.LevelFrom
	}
	if h, ok := skills.Lookup(id); ok && h.LevelFrom != 0 {
		return h.LevelFrom
	}
	return id
}

// attackSpecFor reads the S0a registry: a skill is an attack, summon or field
// object only when its registered handler carries an AttackSpec.
func attackSpecFor(id int) (*combat.AttackSpec, bool) {
	h, ok := skills.Lookup(id)
	if !ok || h.Attack == nil {
		return nil, false
	}
	return h.Attack, true
}

func rollProp(p int) bool { return rand.IntN(100) < p }

// genericAttackGameplay validates any registered attack under the room lock,
// plans ceilings/cost/statuses with internal/combat, and settles through the
// shared combat transaction (family CombatSkill).
func (s *Server) genericAttackGameplay(ctx context.Context, channel int, live *liveCharacter, sink packetSink, a protocol.Attack) error {
	return s.genericAttackGameplayThen(ctx, channel, live, sink, a, nil)
}

func (s *Server) genericAttackGameplayThen(ctx context.Context, channel int, live *liveCharacter, sink packetSink, a protocol.Attack, then func()) error {
	repo, ok := s.repo.(domain.CombatSettlementRepository)
	if !ok || s.gameplay == nil || s.tradeActive(live) {
		return sink.WritePacket(protocol.EnableActions())
	}
	live.persistMu.Lock()
	locked := true
	defer func() {
		if locked {
			live.persistMu.Unlock()
		}
	}()
	live.mu.Lock()
	session := cloneCombatSession(live.session)
	movedAt := live.movedAt
	live.mu.Unlock()
	session.Lease = s.currentLease()
	c := session.Character
	reject := func(reason string, err error) error {
		slog.Warn("attack_rejected", "character", c.ID, "skill", a.SkillID, "opcode", a.Opcode, "reason", reason, "error", err, "targets", len(a.Targets), "tick", a.Tick, "x", a.X, "y", a.Y, "server_x", c.X, "server_y", c.Y)
		return sink.WritePacket(protocol.UpdateHPMP(c.HP, c.MP))
	}
	now := time.Now()
	kind := combat.KindForOpcode(a.Opcode)
	if c.HP <= 0 || kind == 0 {
		return reject("dead_or_kind", nil)
	}
	spec, effect, level, ok := resolveAttackEffect(c, &a, now)
	if !ok {
		if a.SkillID != 0 && attackOnCooldown(c, a.SkillID, now) {
			return reject("cooldown", nil)
		}
		return reject("skill_not_learned_or_unregistered", nil)
	}
	if !within(c.X, c.Y, int(a.X), int(a.Y), attackReach(a.SkillID, effect)+playerDrift(movedAt, now)) {
		return reject("position", nil)
	}
	stats, err := combat.AttackStats(c, now)
	if err != nil {
		return reject("stats", err)
	}
	if kind == combat.Shoot {
		stats.WATK += projectileAttack(c, int(a.Slot))
	}
	var mesos []int64
	if len(a.MesoDrops) > 0 {
		if spec == nil || !spec.ConsumeMesos {
			return reject("meso_drops_not_allowed", nil)
		}
		if mesos, err = s.consumeAttackMesos(ctx, channel, session, a); err != nil {
			return reject("meso_consume", err)
		}
	}
	g := s.gameplay
	key := mapKey{channel: channel, mapID: c.MapID}
	bodies := map[int32]content.MobBodyBounds{}
	for _, t := range a.Targets {
		g.mu.Lock()
		var mobID int
		if room := g.rooms[key]; room != nil && room.mobs[t.ObjectID] != nil {
			mobID = room.mobs[t.ObjectID].stats.ID
		}
		g.mu.Unlock()
		if mobID != 0 {
			if b, e := g.catalog.MobBodyBounds(mobID); e == nil {
				bodies[t.ObjectID] = b
			}
		}
	}
	g.mu.Lock()
	room := g.rooms[key]
	if room == nil || room.members[c.ID] == nil || room.members[c.ID].live != live || !sameDropSink(room.members[c.ID].sink, sink) || room.members[c.ID].sessionID != session.SessionID || room.members[c.ID].generation != session.Generation {
		g.mu.Unlock()
		return reject("scene_stale", nil)
	}
	member := room.members[c.ID]
	eruptAreas, err := ownedEruptionAreasLocked(room, member, session, a, effect, now)
	if err != nil {
		g.mu.Unlock()
		if err := reject("eruption_areas", err); err != nil {
			return err
		}
		return sink.WritePacket(protocol.EnableActions())
	}
	// A different skill may follow within 200ms: final attacks (Evan Dragon
	// Spark 22150004) and Shadow Partner follow-ups are triggered by the
	// client right after the main attack. The client tick is not a duplicate
	// test: keydown attacks repeat one tick per frame (see nextAttackKey).
	if member.attacked && member.lastSkill == a.SkillID && now.Sub(member.lastAttack) < attackInterval(a.SkillID) {
		g.mu.Unlock()
		return reject("attack_rate", nil)
	}
	if kind != combat.Magic && effect.Element == combat.Physical {
		// A charge (including a party member's Thunder Charge) elementalizes
		// physical attacks of every job; skills may still override in Prepare.
		if e, ok := combat.ChargeElement(c, now); ok {
			effect.Element = e
		}
	}
	actx := combat.AttackContext{Kind: kind, Character: c, Stats: stats, Effect: effect, Attack: a, Now: now, Mesos: mesos}
	mobs := make([]*gameplayMob, 0, len(a.Targets))
	for _, t := range a.Targets {
		mob := room.mobs[t.ObjectID]
		if mob == nil || mob.hp <= 0 || mob.claiming {
			g.mu.Unlock()
			return reject("target_missing", nil)
		}
		info := combat.TargetInfo{ObjectID: mob.objectID, Mob: mob.stats, X: mob.life.X, Y: mob.life.Y, HP: mob.hp, Statuses: mob.statuses}
		if b, ok := bodies[t.ObjectID]; ok && b.Envelope.Nonempty() {
			info.HalfWidth = (b.Envelope.Right - b.Envelope.Left) / 2
			info.BodyHeight = max(0, -b.Envelope.Top)
		}
		actx.Targets = append(actx.Targets, info)
		mobs = append(mobs, mob)
	}
	plan, err := combat.PlanAttack(spec, actx, rollProp)
	if err != nil {
		g.mu.Unlock()
		return reject("plan", err)
	}
	cost := &domain.CombatCost{Opcode: a.Opcode, MP: plan.Cost.MP, HP: plan.Cost.HP, Level: level, CooldownMS: int(plan.Cooldown.Milliseconds()), HPGain: plan.Heal, MPGain: plan.MPGain, Meso: plan.Cost.Money}
	if attackOngoing(c, a, now) {
		cost.CooldownMS, cost.Ongoing = 0, true // The cast already started it.
	}
	gen := &genericAttack{attack: a, level: level, bulletItem: -1, then: then, eruptAreas: eruptAreas}
	if kind != combat.Magic {
		gen.bulletItem = a.Charge
	}
	if plan.Cost.Items > 0 {
		// A fixed consumable (itemCon) replaces projectiles for this attack.
		item, ok := itemStack(c, plan.Cost.ItemID, plan.Cost.Items)
		if !ok {
			g.mu.Unlock()
			return reject("item_cost", nil)
		}
		cost.AmmoItemID, cost.AmmoSlot, cost.Ammo = item.TemplateID, item.Slot, plan.Cost.Items
	} else if plan.Cost.Bullets > 0 {
		item, ok := ammoAt(c, int(a.Slot), plan.Cost.Ammo, plan.Cost.Bullets)
		if !ok {
			g.mu.Unlock()
			return reject("ammo", nil)
		}
		cost.AmmoItemID, cost.AmmoSlot, cost.Ammo = item.TemplateID, item.Slot, plan.Cost.Bullets
		gen.bulletItem = int32(item.TemplateID)
	}
	family := domain.CombatSkill
	attackKey := live.nextAttackKey(now)
	ticket := &combatTicket{actor: live, sink: sink, actorSession: session, key: key, generic: gen,
		request: domain.CombatAttackRequest{Session: session, OperationID: fmt.Sprintf("combat-%s-%d-%d-%d", session.SessionID, session.Generation, attackKey, family), ClientTick: attackKey, Family: family, SkillID: a.SkillID, LinesPerTarget: a.LineCount(), Cost: cost, MesoDrops: a.MesoDrops}}
	if len(eruptAreas) > 0 {
		ticket.expectedActorEntry = member
		// Bind the explicit area list to the immutable settlement manifest.
		ticket.request.Wire = append([]byte(nil), a.Wire...)
		for _, area := range eruptAreas {
			area.reservation = ticket
		}
	}
	killed := false
	for i, mob := range mobs {
		tp := plan.Targets[i]
		audit := criticalAudits.forTarget(mob.stats.Boss, stats.BossCritRate)
		targetCritRate := stats.CriticalRateAgainst(actx.Effect.CriticalRate, mob.stats.Boss)
		for j, over := range tp.Critical {
			if !over {
				if tp.Lines[j] > 0 {
					audit.Normal(c.ID)
				}
				continue
			}
			if !audit.Admit(c.ID, targetCritRate) {
				slog.Warn("attack_crit_excess", "character", c.ID, "skill", a.SkillID, "claimed", tp.Lines[j], "bound", tp.Normal[j], "crit_rate", targetCritRate)
				tp.Lines[j], tp.Critical[j] = min(tp.Lines[j], tp.Normal[j]), false
				tp.Clipped++
			}
		}
		lines := append([]int(nil), tp.Lines...)
		damage := int(combat.ClipToHP(lines, mob.lethalHP()))
		q := domain.CombatTarget{ObjectID: mob.objectID, MobID: mob.stats.ID, SpawnOperationID: fmt.Sprintf("mob-%s-%d-%d-%d", g.bootID, channel, c.MapID, mob.objectID), X: mob.life.X, Y: mob.life.Y, BeforeHP: mob.hp, Damage: damage, Defeated: int64(damage) == mob.hp, Contributions: addCombatDamage(mob.contributions, session, damage)}
		for j, l := range a.Targets[i].Lines {
			q.Lines = append(q.Lines, domain.CombatDamageLine{Raw: l.Raw, Applied: lines[j], Criticality: tp.Criticality[j], Long: l.Long})
			gen.attack.Targets[i].Lines[j].Damage = lines[j]
			if tp.Critical[j] {
				// Broadcast only; the settlement above keeps the raw wire value.
				gen.attack.Targets[i].Lines[j].Raw |= 0x80000000
			}
		}
		for len(q.Lines) < a.LineCount() {
			// Meso Explosion names a per-target line count; settlement wants
			// LinesPerTarget lines on every target.
			q.Lines = append(q.Lines, domain.CombatDamageLine{})
		}
		ticket.request.Targets = append(ticket.request.Targets, q)
		ticket.targets = append(ticket.targets, mob)
		gen.statuses = append(gen.statuses, tp.Statuses)
		gen.clipped += tp.Clipped
		gen.belowFloor += tp.BelowFloor
		killed = killed || q.Defeated
	}
	member.attacked, member.lastTick, member.lastAttack, member.lastSkill = true, a.Tick, now, a.SkillID
	gen.erupt = append([]int(nil), specErupt(spec)...)
	if a.SkillID == 2121003 {
		gen.erupt = nil
	}
	gen.spec = spec
	free := cost.Meso == 0 && cost.MP == 0 && cost.HP == 0 && cost.Ammo == 0 && cost.CooldownMS == 0 && cost.HPGain == 0 && cost.MPGain == 0
	if free && !killed {
		// No durable cost or reward: one room-serialized memory change.
		for i, mob := range ticket.targets {
			q := ticket.request.Targets[i]
			mob.hp -= int64(q.Damage)
			ticket.recordAppliedDamageLocked(q, mob.hp, false)
			mob.contributions = q.Contributions
		}
		s.applyGenericStatusesLocked(ticket, now)
		ticket.observers = combatRoomPeers(room)
		g.mu.Unlock()
		live.persistMu.Unlock()
		locked = false
		s.publishCombatTicket(ctx, ticket, domain.CombatAttackResult{})
		return nil
	}
	for _, mob := range ticket.targets {
		mob.claiming = true
		mob.reservation = ticket
	}
	g.mu.Unlock()
	live.persistMu.Unlock()
	locked = false
	return s.executeCombatTicket(ctx, repo, ticket)
}

// rapidAttacks fire a frame every ~100ms of client time while the key is
// held (Java GameConstants.getAttackDelay 99): Hurricane, Ishtar's Ring,
// Rapid Fire, Wild Hunter and Mechanic machine guns, Mille Aiguilles; the v148
// rush 5101012 lands its hits as separate frames the same way.
var rapidAttacks = map[int]bool{5101012: true, 3121004: true, 13111002: true, 23121000: true, 33121009: true, 5221004: true, 5721001: true, 5201006: true, 5311002: true, 5711002: true, 35001001: true, 35101009: true, 35111004: true, 35121005: true, 35121013: true, 24121000: true, 14111005: true, 4121007: true, 5221007: true}

// attackInterval is the least server-side gap between two attacks of the
// same skill by one character.
func attackInterval(skillID int) time.Duration {
	if rapidAttacks[skillID] {
		return 50 * time.Millisecond
	}
	return 200 * time.Millisecond
}

func specErupt(s *combat.AttackSpec) []int {
	if s == nil {
		return nil
	}
	return s.EruptAreas
}

// itemStack finds a USE stack of the item with at least n.
func itemStack(c domain.Character, itemID, n int) (domain.InventoryItem, bool) {
	for _, it := range c.Items {
		if it.InvType == 2 && it.TemplateID == itemID && it.Quantity >= n {
			return it, true
		}
	}
	return domain.InventoryItem{}, false
}

// ammoAt finds the projectile stack the client named. Arrows must match the
// weapon family exactly; stars/bullets by class.
// projectileAttack is the attack of the stack this shot names, which the client adds
// to its own (Java PlayerHandler.rangedAttack projectileWatk). Only a stack the
// character really holds counts, and the cost check still consumes from that stack.
func projectileAttack(c domain.Character, slot int) int {
	builtIn, _ := content.ZenProjectile(c.Job)
	for _, it := range c.Items {
		if it.InvType != 2 || it.Slot != slot || it.Quantity < 1 || combat.AmmoOf(it.TemplateID) == combat.AmmoNone {
			continue
		}
		if it.TemplateID == builtIn {
			return 0 // BaseStats already includes this projectile's attack.
		}
		if def, err := content.ItemDefinition(os.Getenv("MAPLEGO_WZ_ROOT"), it.TemplateID); err == nil {
			return def.ProjectileAttack
		}
	}
	return 0
}

func ammoAt(c domain.Character, slot int, want combat.Ammo, n int) (domain.InventoryItem, bool) {
	for _, it := range c.Items {
		if it.InvType == 2 && it.Slot == slot && it.Quantity >= n && combat.AmmoOf(it.TemplateID) == want {
			return it, true
		}
	}
	return domain.InventoryItem{}, false
}

// applyGenericStatusesLocked applies planned statuses to surviving targets
// and detonates the attacker's listed areas. Caller holds gameplay.mu.
func (s *Server) applyGenericStatusesLocked(t *combatTicket, now time.Time) {
	gen := t.generic
	if gen == nil {
		return
	}
	defer releaseEruptionClaimsLocked(t)
	gen.statusOut = nil
	if room := s.gameplay.rooms[t.key]; room != nil && room.members[t.actorSession.CharacterID] == t.expectedActorEntry {
		for _, a := range gen.eruptAreas {
			if room.areas[a.objectID] == a && a.reservation == t {
				delete(room.areas, a.objectID)
				a.reservation = nil
				gen.extraOut = append(gen.extraOut, protocol.AffectedAreaRemoved(a.objectID, a.skillID, true))
			}
		}
	}
	landed := false
	for _, q := range t.request.Targets {
		landed = landed || q.Damage > 0
	}
	if room := s.gameplay.rooms[t.key]; room != nil && landed {
		for id, a := range room.areas {
			for _, sk := range gen.erupt {
				if a.ownerID == t.actorSession.CharacterID && a.skillID == sk {
					delete(room.areas, id)
					gen.extraOut = append(gen.extraOut, protocol.AffectedAreaRemoved(a.objectID, a.skillID, true))
				}
			}
		}
	}
	for i, mob := range t.targets {
		if i >= len(gen.statuses) || mob.hp <= 0 {
			continue
		}
		if p := applyMobStatusesLocked(mob, gen.statuses[i], t.actorSession, now); p != nil {
			gen.statusOut = append(gen.statusOut, p)
		}
	}
}

func (gen *genericAttack) remotePacket(c domain.Character, long *protocol.LongDamageOut) []byte {
	if gen.serverDamage {
		return nil // Server-originated damage shows only the damage numbers.
	}
	if gen.summon != nil {
		hits := make([]protocol.SummonHit, 0, len(gen.attack.Targets))
		for _, t := range gen.attack.Targets {
			hits = append(hits, protocol.SummonHit{ObjectID: t.ObjectID, Damage: t.Lines[0].Damage})
		}
		return protocol.SummonAttackPacketLong(gen.summon.ownerID, gen.summon.objectID, gen.summonOwnerLevel, gen.summonAction, hits, long)
	}
	return protocol.RemoteAttackPacket(c, protocol.RemoteAttack{Opcode: protocol.RemoteAttackOpcode(gen.attack.Opcode), SkillLevel: gen.level, BulletItem: gen.bulletItem, Attack: gen.attack, Long: long})
}

// serverLineLong records a server-authored line whose damage exceeds its Raw.
func serverLineLong(damage int) int64 {
	if damage > 0x7fffffff {
		return int64(damage)
	}
	return 0
}

// genericAttackRoute decodes body with the generic codec and handles it
// unless a reviewed legacy family owns it. handled=false leaves the legacy
// decoder to read the same body.
func (s *Server) genericAttackRoute(ctx context.Context, channel int, live *liveCharacter, sink packetSink, op uint16, body []byte) (bool, error) {
	a, err := protocol.ReadAttack(op, protocol.NewReader(body))
	live.longDamage.Apply(a.Targets)
	live.mu.Lock()
	job, characterID := live.session.Character.Job, live.session.CharacterID
	routeCharacter := live.session.Character
	routeCharacter.Equipment = append([]domain.Equipment(nil), routeCharacter.Equipment...)
	live.mu.Unlock()
	if err != nil {
		if attackParseDiag.Add(1) <= 50 {
			slog.Warn("attack_parse_rejected", "character", characterID, "opcode", op, "job", job, "skill", a.SkillID, "hex", hex.EncodeToString(body))
		}
		if op == protocol.RecvShootAttack || op == protocol.RecvBodyAttack {
			return true, sink.WritePacket(protocol.EnableActions())
		}
		return false, nil
	}
	if legacyAttack(op, job, a.SkillID) {
		generic := false
		if op == protocol.RecvMeleeAttack {
			// Generic ReadAttack has already preserved every native raw line.
			// The signed-I32 compatibility codec cannot carry a critical flag;
			// never send such a valid frame back through that decoder.
			for _, target := range a.Targets {
				for _, line := range target.Lines {
					generic = generic || line.Critical() || line.Token()
				}
			}
			// This is route selection only. The generic handler rechecks the
			// current state under persistMu before any cost or damage. Even an
			// invalid/pending potential must reach that checked rejection path,
			// not an equipment-only compatibility fallback.
			if effects, sourceErr := content.ResolvePotentialEffects(os.Getenv("MAPLEGO_WZ_ROOT"), routeCharacter, time.Now()); sourceErr != nil || len(effects.Lines) > 0 {
				generic = true
			}
		}
		if !generic {
			return false, nil
		}
	}
	return true, s.genericAttackGameplay(ctx, channel, live, sink, a)
}

// attackAppliedDiag bounds the acceptance log of applied generic attacks.
var attackAppliedDiag atomic.Int32

// attackParseDiag bounds the logged attack frames the parser rejects.
var attackParseDiag atomic.Int32

// consumeAttackMesos retires the named ground mesos for a Meso Explosion
// style attack and shows them exploding to the room.
func (s *Server) consumeAttackMesos(ctx context.Context, channel int, session domain.CharacterSession, a protocol.Attack) ([]int64, error) {
	repo, ok := s.repo.(domain.SkillMesoRepository)
	if !ok {
		return nil, fmt.Errorf("skill meso repository unavailable")
	}
	session.ChannelID = channel
	step, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	// Only read here: the settlement retires the drops with the attack.
	drops, err := repo.PeekMesoDrops(step, domain.MesoConsumeRequest{Session: session, SkillID: a.SkillID, ObjectIDs: a.MesoDrops})
	if err != nil {
		return nil, err
	}
	out := make([]int64, 0, len(drops))
	for _, d := range drops {
		out = append(out, d.Mesos)
	}
	return out, nil
}

// attackMesoSpawner gives AfterHit a bounded way to drop skill mesos
// (Pickpocket); each call is a distinct durable operation.
func (s *Server) attackMesoSpawner(ctx context.Context, t *combatTicket) func(x, y int, amount int64) error {
	repo, ok := s.repo.(domain.SkillMesoRepository)
	if !ok {
		return nil
	}
	seq := 0
	return func(x, y int, amount int64) error {
		seq++
		session := t.actorSession
		session.ChannelID = t.key.channel
		op := fmt.Sprintf("meso-skill-%s-%d-%d-%d", t.request.OperationID, t.request.SkillID, seq, amount)
		step, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		drop, err := repo.SpawnSkillMesos(step, domain.SkillMesoSpawnRequest{Session: session, OperationID: op, SkillID: t.request.SkillID, X: x, Y: y, Amount: amount})
		if err != nil {
			return err
		}
		s.broadcastInventoryPacket(drop.ChannelID, drop.MapID, protocol.DropInventoryItem(drop))
		return nil
	}
}

// DamageMobs applies server-computed skill damage (counter-attacks fired from
// the damage path) to monsters in the caller's room and settles kills like
// any attack. The skill must be learned; each hit is capped by the line
// display cap and the monster's HP. S0a calls it from takeDamage after
// releasing live.persistMu (this function takes it).
func (s *Server) DamageMobs(ctx context.Context, channel int, live *liveCharacter, sink packetSink, skillID int, hits []combat.ServerHit) error {
	repo, ok := s.repo.(domain.CombatSettlementRepository)
	if !ok || s.gameplay == nil || len(hits) == 0 || len(hits) > 15 {
		return nil
	}
	live.persistMu.Lock()
	locked := true
	defer func() {
		if locked {
			live.persistMu.Unlock()
		}
	}()
	live.mu.Lock()
	session := cloneCombatSession(live.session)
	live.mu.Unlock()
	session.Lease = s.currentLease()
	c := session.Character
	now := time.Now()
	level := attackSkillLevel(c, skillID, now)
	if c.HP <= 0 || level < 1 {
		return nil
	}
	g := s.gameplay
	key := mapKey{channel: channel, mapID: c.MapID}
	g.mu.Lock()
	room := g.rooms[key]
	if room == nil || room.members[c.ID] == nil || room.members[c.ID].live != live {
		g.mu.Unlock()
		return nil
	}
	tick := uint32(now.UnixMilli())
	gen := &genericAttack{level: level, serverDamage: true, attack: protocol.Attack{SkillID: skillID, TByte: byte(len(hits)<<4 | 1), Tick: tick}}
	ticket := &combatTicket{actor: live, sink: sink, actorSession: session, key: key, generic: gen,
		request: domain.CombatAttackRequest{Session: session, OperationID: fmt.Sprintf("server-hit-%s-%d-%d-%d", session.SessionID, session.Generation, skillID, now.UnixNano()), ClientTick: tick, Family: domain.CombatSkill, SkillID: skillID, LinesPerTarget: 1, Cost: &domain.CombatCost{Level: level}}}
	seen := map[int32]bool{}
	killed := false
	for _, h := range hits {
		mob := room.mobs[h.ObjectID]
		if seen[h.ObjectID] || mob == nil || mob.hp <= 0 || mob.claiming || h.Damage <= 0 || mob.stats.Invincible {
			continue
		}
		seen[h.ObjectID] = true
		damage := int(min(int64(min(h.Damage, combat.MaxLineDamage())), mob.lethalHP()))
		q := domain.CombatTarget{ObjectID: mob.objectID, MobID: mob.stats.ID, SpawnOperationID: fmt.Sprintf("mob-%s-%d-%d-%d", g.bootID, channel, c.MapID, mob.objectID), X: mob.life.X, Y: mob.life.Y, BeforeHP: mob.hp, Damage: damage, Defeated: int64(damage) == mob.hp, Contributions: addCombatDamage(mob.contributions, session, damage),
			Lines: []domain.CombatDamageLine{{Raw: uint32(min(damage, 0x7fffffff)), Applied: damage, Long: serverLineLong(damage)}}}
		gen.attack.Targets = append(gen.attack.Targets, protocol.AttackTarget{ObjectID: mob.objectID, Lines: []protocol.AttackLine{{Raw: uint32(min(damage, 0x7fffffff)), Damage: damage}}})
		ticket.request.Targets = append(ticket.request.Targets, q)
		ticket.targets = append(ticket.targets, mob)
		killed = killed || q.Defeated
	}
	if len(ticket.targets) == 0 {
		g.mu.Unlock()
		return nil
	}
	gen.attack.TByte = byte(len(ticket.targets)<<4 | 1)
	if !killed {
		for i, mob := range ticket.targets {
			q := ticket.request.Targets[i]
			mob.hp -= int64(q.Damage)
			ticket.recordAppliedDamageLocked(q, mob.hp, false)
			mob.contributions = q.Contributions
		}
		ticket.observers = combatRoomPeers(room)
		g.mu.Unlock()
		live.persistMu.Unlock()
		locked = false
		s.publishCombatTicket(ctx, ticket, domain.CombatAttackResult{})
		return nil
	}
	for _, mob := range ticket.targets {
		mob.claiming = true
		mob.reservation = ticket
	}
	g.mu.Unlock()
	live.persistMu.Unlock()
	locked = false
	return s.executeCombatTicket(ctx, repo, ticket)
}

// attackStealer gives AfterHit Steal: one pinned drop-table item per spawn.
func (s *Server) attackStealer(ctx context.Context, t *combatTicket) func(h combat.HitInfo) error {
	repo, ok := s.repo.(domain.SkillMesoRepository)
	if !ok {
		return nil
	}
	return func(h combat.HitInfo) error {
		session := t.actorSession
		session.ChannelID = t.key.channel
		step, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		drop, err := repo.StealMobItem(step, domain.MobStealRequest{Session: session, OperationID: "steal-" + t.request.OperationID + "-" + h.SpawnOperationID, SkillID: t.request.SkillID, MobID: h.MobID, SpawnOperationID: h.SpawnOperationID, X: h.X, Y: h.Y, Boss: h.Boss})
		if err != nil || drop == nil {
			return err
		}
		s.broadcastInventoryPacket(drop.ChannelID, drop.MapID, protocol.DropInventoryItem(*drop))
		return nil
	}
}

// traceCharacterPackets starts only after the authenticated character is known.
// Full plaintext capture is an additional opt-in for those exact QA identities.
func traceCharacterPackets(c *protocol.Conn, characterID int64) {
	if !traceCharacterEnabled(characterID) {
		return
	}
	full := os.Getenv("MAPLEGO_TRACE_FULL_PACKETS") == "1"
	c.SetTrace(func(p []byte) { recordCharacterPacket(slog.Default(), "trace_out", characterID, full, p) })
}

func traceCharacterEnabled(characterID int64) bool {
	if characterID <= 0 {
		return false
	}
	for _, v := range strings.Split(os.Getenv("MAPLEGO_TRACE_CHARACTERS"), ",") {
		if id, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil && id == characterID {
			return true
		}
	}
	return false
}

func recordCharacterPacket(logger *slog.Logger, direction string, characterID int64, full bool, p []byte) {
	if len(p) < 2 || len(p) > protocol.MaxPacketSize {
		return
	}
	n := min(len(p), 96)
	hash := sha256.Sum256(p)
	fields := []any{"character", characterID, "opcode", fmt.Sprintf("0x%x", uint16(p[0])|uint16(p[1])<<8), "len", len(p), "hex", hex.EncodeToString(p[:n]), "wire_sha256", hex.EncodeToString(hash[:]), "full_packet", full}
	if full {
		fields = append(fields, "wire_hex", hex.EncodeToString(p))
	}
	logger.Info(direction, fields...)
}

// A player keeps moving between movement reports, so the attack position runs
// ahead of the stored one: a jump or fall covers up to playerDriftSpeed (the
// client's terminal fall speed, 670) until the next report arrives.
const (
	playerDriftSpeed  = 670 // pixels a second
	playerDriftWindow = 500 * time.Millisecond
)

// playerDrift is how far the character may be from its stored position at now.
func playerDrift(movedAt, now time.Time) int {
	since := playerDriftWindow
	if !movedAt.IsZero() {
		since = min(max(now.Sub(movedAt), 0), playerDriftWindow)
	}
	return int(since * playerDriftSpeed / time.Second)
}

// attackReach is how far the attack position may be from the server position:
// WZ info/casterMove skills (Demon Lash 31101001/31101002, Aran rushes) move
// the caster before the hit, up to their attack box; Java checks neither.
func attackReach(skillID int, e combat.Effect) int {
	const reach = 120
	if skillID == 0 {
		return reach
	}
	def, err := content.SkillDefinition(os.Getenv("MAPLEGO_WZ_ROOT"), skillID)
	if err != nil || def.Info["casterMove"] == 0 && !teleportMastery[skillID] {
		return reach
	}
	r := max(e.Range, 300)
	if e.Box.Set {
		r = max(r, -e.Box.LT.X, e.Box.RB.X, -e.Box.LT.Y, e.Box.RB.Y)
	}
	return reach + r
}

// moveParseDiag bounds the logged player movement frames the parser rejects.
var moveParseDiag atomic.Int32

func liveCharacterID(live *liveCharacter) int64 {
	live.mu.Lock()
	defer live.mu.Unlock()
	return live.session.CharacterID
}

// attackOngoing reports attacks that belong to an active cast: keydown body
// attacks (0x4E, Hurricane 32121003 every 0.5s) and attacks of a skill whose
// own buff is running. Their cooldown was started by the cast, so it neither
// blocks them nor restarts.
func attackOngoing(c domain.Character, a protocol.Attack, now time.Time) bool {
	if a.SkillID == 0 {
		return false
	}
	if a.Opcode == protocol.RecvBodyAttack {
		return true
	}
	for _, b := range c.SkillBuffs {
		if b.ID == a.SkillID && b.ExpiresAt.After(now) {
			return true
		}
	}
	return false
}

// teleportMastery attacks are sent from the teleport destination before the
// movement packet arrives (TELEPORT_MASTERY body attacks, 0x4E).
var teleportMastery = map[int]bool{2111007: true, 2211007: true, 2311007: true, 12111007: true, 22161005: true, 32111010: true}
