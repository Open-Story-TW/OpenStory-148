package world

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"sync"
	"time"

	"maple-go-server/internal/combat"
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

type gameplayMob struct {
	objectID      int32
	life          content.Life
	origin        content.Life
	stats         content.Mob
	hp            int64
	controller    int64
	escortHitAt   time.Time // last collision this mob reported against the escort
	respawnAt     time.Time
	claiming      bool
	contributions []domain.CombatContribution
	reservation   *combatTicket
	statuses      combat.MobStatuses
	prelude       bool      // An event opening may take damage without a defeat.
	qaHeld        bool      // QA !mobhold: never defeated, refilled every tick.
	qaHeldUntil   time.Time // the hold lapses here (mobHoldTerm)
	qaOwner       *qaMobOwner
}

// Event maps reset their one-time spawns between groups.
func (m *gameplayMob) respawnTime(mapID int, now time.Time) time.Time {
	_, eventMap := content.EventByMap(mapID)
	if eventMap && m.life.RespawnSeconds == -1 {
		return time.Time{}
	}
	return now.Add(time.Duration(max(5, m.life.RespawnSeconds)) * time.Second)
}

// lethalHP is the most damage one settlement may apply: a held mob keeps 1 HP.
func (m *gameplayMob) lethalHP() int64 {
	if m.qaHeld || m.prelude {
		return m.hp - 1
	}
	return m.hp
}

type gameplayMember struct {
	visit      *sceneVisit // Exact entry token for scene-safe QA expiry publication.
	sink       packetSink
	live       *liveCharacter
	sessionID  string
	generation int64
	worldID    int
	lastAttack time.Time
	lastTick   uint32
	lastSkill  int // Skill of the last generic attack (rate guard).
	// keydownSkill is the charging skill from 0x97, started at keydownAt.
	keydownSkill  int
	keydownAt     time.Time
	attacked      bool
	barrelRelease *barrelReleaseTicket // Exact visit's last native CGrenade release.
}
type gameplayRoom struct {
	definition content.Map
	members    map[int64]*gameplayMember
	npcs       map[int32]content.Life
	mobs       map[int32]*gameplayMob
	summons    map[int32]*roomSummon
	areas      map[int32]*roomArea
	reactors   map[int32]*roomReactor // gathering nodes and event reactors
	moon       *henesysRoom
}
type gameplaySpace struct {
	mu            sync.Mutex
	catalog       *content.Catalog
	rooms         map[mapKey]*gameplayRoom
	nextID        int32
	bootID        string
	pendingCombat map[string]*combatTicket
	doors         map[doorKey]*townPortal
	qaMobOutcomes map[string]qaMobOutcome
}

func newGameplaySpace() (*gameplaySpace, error) {
	c, err := content.DefaultCatalog()
	if err != nil {
		return nil, err
	}
	var nonce [16]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	return &gameplaySpace{catalog: c, rooms: make(map[mapKey]*gameplayRoom), nextID: 1000000, bootID: hex.EncodeToString(nonce[:])}, nil
}

func (g *gameplaySpace) alloc() int32 {
	if g.nextID >= 999999999 {
		return 0
	}
	g.nextID++
	return g.nextID
}
func (g *gameplaySpace) room(key mapKey) (*gameplayRoom, error) {
	if r := g.rooms[key]; r != nil {
		return r, nil
	}
	m, err := g.catalog.Map(key.mapID)
	if err != nil {
		return nil, err
	}
	r := &gameplayRoom{definition: m, members: make(map[int64]*gameplayMember), npcs: make(map[int32]content.Life), mobs: make(map[int32]*gameplayMob)}
	for _, life := range activityLife(m) {
		if life.Hidden {
			continue
		}
		if life.Kind == "n" {
			oid := g.alloc()
			if oid == 0 {
				return nil, errors.New("gameplay object ID space exhausted")
			}
			r.npcs[oid] = life
			continue
		}
		if life.Kind != "m" {
			continue
		}
		stats, err := g.catalog.Mob(life.TemplateID)
		if err != nil {
			return nil, err
		}
		oid := g.alloc()
		if oid == 0 {
			return nil, errors.New("gameplay object ID space exhausted")
		}
		r.mobs[oid] = &gameplayMob{objectID: oid, life: life, origin: life, stats: stats, hp: stats.HP}
	}
	g.addReactorsLocked(r, key.mapID)
	g.rooms[key] = r
	return r, nil
}

func (s *Server) enterGameplay(channel int, live *liveCharacter, sink packetSink) error {
	if s.gameplay == nil {
		return nil
	}
	live.mu.Lock()
	entrySession := live.session
	c := entrySession.Character
	live.mu.Unlock()
	party := s.partyOf(c.ID)
	visit := s.sceneFor(live, sink)
	g := s.gameplay
	g.mu.Lock()
	r, err := g.room(mapKey{channel: channel, mapID: c.MapID})
	if err != nil {
		g.mu.Unlock()
		return err
	}
	r.members[c.ID] = &gameplayMember{sink: sink, live: live, sessionID: entrySession.SessionID, generation: entrySession.Generation, worldID: entrySession.Lease.WorldID, visit: visit}
	now := time.Now()
	packets := s.npcScriptability(r.npcs)
	for oid, npc := range r.npcs {
		packets = append(packets, protocol.SpawnNPC(oid, npc, false), protocol.SpawnNPC(oid, npc, true))
	}
	for _, mob := range r.mobs {
		if mob.hp <= 0 {
			continue
		}
		packets = append(packets, mob.spawnPacket(false, now))
		if mob.controller == 0 {
			mob.controller = c.ID
		}
		if mob.controller == c.ID {
			packets = append(packets, mob.spawnPacket(true, now))
		}
	}
	packets = append(packets, summonEnterPacketsLocked(r)...)
	packets = append(packets, areaEnterPacketsLocked(r)...)
	packets = append(packets, reactorEnterPacketsLocked(r, now)...)
	packets = append(packets, g.doorEnterPacketsLocked(channel, c.MapID, c.ID, party)...)
	g.mu.Unlock()
	for _, p := range packets {
		if err := sink.WritePacket(p); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) leaveGameplay(channel, mapID int, characterID int64, expected ...packetSink) {
	if s.gameplay == nil {
		return
	}
	g := s.gameplay
	g.mu.Lock()
	r := g.rooms[mapKey{channel: channel, mapID: mapID}]
	if r == nil {
		g.mu.Unlock()
		return
	}
	if len(expected) > 0 {
		member := r.members[characterID]
		if member == nil || !sameDropSink(member.sink, expected[0]) {
			g.mu.Unlock()
			return
		}
	}
	departed := r.members[characterID]
	delete(r.members, characterID)
	retireQAMobOwnerLocked(r, departed)
	type delivery struct {
		sink   packetSink
		packet []byte
	}
	var packets []delivery
	for _, d := range removeOwnerSummonsLocked(r, characterID, 0) {
		packets = append(packets, delivery{d.sink, d.packet})
	}
	for _, p := range releaseHarvestLocked(r, characterID) {
		for _, m := range r.members {
			packets = append(packets, delivery{m.sink, p})
		}
	}
	for _, mob := range r.mobs {
		if mob.qaOwner != nil && mob.qaOwner.retired {
			continue
		}
		if mob.controller != characterID {
			continue
		}
		mob.controller = 0
		for id, m := range r.members {
			mob.controller = id
			if mob.hp > 0 {
				packets = append(packets, delivery{m.sink, mob.spawnPacket(true, time.Now())})
			}
			break
		}
	}
	g.mu.Unlock()
	for _, d := range packets {
		_ = d.sink.WritePacket(d.packet)
	}
}

func (s *Server) runGameplay(ctx context.Context) {
	if s.gameplay == nil {
		return
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			s.expireQAMobs(now)
			s.gameplay.respawn(now)
			s.gameplay.tickMobStatuses(now)
			s.gameplay.refillHeldMobs()
			s.gameplay.tickSummons(now)
			s.gameplay.tickAreas(now)
			s.gameplay.tickDoors(now, s.partySnapshot())
			s.gameplay.tickReactors(now)
			s.tickEvents(now)
			s.tickHenesysPQ(ctx, now)
		}
	}
}

func (g *gameplaySpace) respawn(now time.Time) {
	type delivery struct {
		sink   packetSink
		packet []byte
	}
	var packets []delivery
	g.mu.Lock()
	for _, r := range g.rooms {
		if len(r.members) == 0 {
			continue
		}
		var dead []*gameplayMob
		for _, mob := range r.mobs {
			if mob.qaOwner != nil && mob.hp <= 0 && !mob.claiming && mob.reservation == nil {
				delete(r.mobs, mob.objectID)
				continue
			}
			if mob.hp <= 0 && !mob.claiming && !mob.respawnAt.IsZero() && !now.Before(mob.respawnAt) {
				dead = append(dead, mob)
			}
		}
		for _, mob := range dead {
			out, ok := g.respawnMobLocked(r, mob)
			if !ok {
				continue
			}
			for _, d := range out {
				packets = append(packets, delivery{d.sink, d.packet})
			}
		}
	}
	g.mu.Unlock()
	for _, d := range packets {
		_ = d.sink.WritePacket(d.packet)
	}
}

func (s *Server) handleGameplay(ctx context.Context, channel int, live *liveCharacter, sink packetSink, op uint16, r *protocol.Reader) (bool, error) {
	if s.gameplay == nil {
		return false, nil
	}
	switch op {
	case protocol.RecvScriptPortal:
		req, err := protocol.ReadScriptPortalRequest(r)
		if err != nil {
			return true, err
		}
		return true, s.scriptPortalGameplay(ctx, channel, live, sink, req)
	case protocol.RecvChangeMap:
		req, err := protocol.ReadPortalRequest(r)
		if err != nil {
			slog.Info("change_map_malformed", "character", live.session.CharacterID, "target", req.TargetMap, "portal", req.Name, "error", err)
			return true, err
		}
		return true, s.portalGameplay(ctx, channel, live, sink, req)
	case protocol.RecvNPCTalk:
		oid := r.I32()
		if r.Err() != nil || r.Remaining() > 32 {
			return true, protocol.ErrMalformed
		}
		r.Skip(r.Remaining())
		return true, s.npcGameplay(channel, live, sink, oid)
	case protocol.RecvNPCTalkMore:
		// Current information-only dialog has one page and no rewards or hidden transitions.
		if r.Remaining() < 2 || r.Remaining() > 32 {
			return true, protocol.ErrMalformed
		}
		r.Skip(r.Remaining())
		return true, sink.WritePacket(protocol.EnableActions())
	case protocol.RecvShootAttack, protocol.RecvBodyAttack:
		body := r.Bytes(r.Remaining())
		if handled, err := s.genericAttackRoute(ctx, channel, live, sink, op, body); handled {
			return true, err
		}
		return true, sink.WritePacket(protocol.EnableActions())
	case protocol.RecvSkillPrepare:
		s.skillPrepare(channel, live, r.Bytes(r.Remaining()))
		return true, nil
	case protocol.RecvThrowGrenade:
		s.throwGrenade(ctx, channel, live, sink, r.Bytes(r.Remaining()))
		return true, nil
	case protocol.RecvAranCombo:
		r.Skip(r.Remaining())
		s.clientCombo(live, sink)
		return true, nil
	case protocol.RecvMoveDragon:
		s.dragonMove(channel, live, sink, r.Bytes(r.Remaining()))
		return true, nil
	case protocol.RecvUseTownPortal, protocol.RecvMoveSummon, protocol.RecvSummonAttack, protocol.RecvDamageSummon, protocol.RecvSubSummon, protocol.RecvRemoveSummon:
		// Parse a detached copy: the connection loop closes the socket when
		// its reader is in error, and these layouts are Java-derived until
		// MCP-confirmed, so a mismatch must drop the packet, not the client.
		body := r.Bytes(r.Remaining())
		return true, s.summonFieldPacket(ctx, channel, live, sink, op, body)
	case protocol.RecvMeleeAttack:
		body := r.Bytes(r.Remaining())
		if handled, err := s.genericAttackRoute(ctx, channel, live, sink, op, body); handled {
			return true, err
		}
		r = protocol.NewReader(body)
		a, err := protocol.ReadMeleeAttack(r)
		if err != nil {
			return true, sink.WritePacket(protocol.EnableActions())
		}
		if a.Skill != 0 {
			return true, s.physicalSkillGameplay(ctx, channel, live, sink, a)
		}
		return true, s.meleeGameplay(ctx, channel, live, sink, a)
	case protocol.RecvStartHarvest, protocol.RecvStopHarvest, protocol.RecvReactorHit, protocol.RecvProfessionInfo,
		protocol.RecvCraftEffect, protocol.RecvCraftMake, protocol.RecvCraftDone, protocol.RecvUseRecipe:
		return true, s.handleProfession(ctx, channel, live, sink, op, r.Bytes(r.Remaining()))
	case protocol.RecvMoveMonster:
		m, err := protocol.ReadMonsterMove(r)
		if err != nil {
			return true, err
		}
		return true, s.monsterMovement(channel, live, sink, m)
	case protocol.RecvMobHitEscort:
		hit, err := protocol.ReadMobHitEscort(protocol.NewReader(r.Bytes(r.Remaining())))
		if err == nil {
			s.moonBunnyHit(channel, live, sink, hit, time.Now())
		}
		return true, nil
	}
	return false, nil
}

func (s *Server) scriptPortalGameplay(ctx context.Context, channel int, live *liveCharacter, sink packetSink, req protocol.ScriptPortalRequest) error {
	if s.tradeActive(live) {
		return sink.WritePacket(protocol.EnableActions())
	}
	live.mu.Lock()
	chr := live.session.Character
	live.mu.Unlock()
	m, err := s.gameplay.catalog.Map(chr.MapID)
	if err != nil {
		return err
	}
	p, ok := m.Portal(req.Name)
	// Only a script in the current server map may be invoked. In particular,
	// the client coordinates cannot authorize a distant or ordinary portal.
	if !ok || p.Script == "" || chr.HP <= 0 || !within(chr.X, chr.Y, p.X, p.Y, 150) {
		return sink.WritePacket(protocol.EnableActions())
	}
	return s.runPortalScript(ctx, channel, live, sink, p)
}

func (s *Server) portalGameplay(ctx context.Context, channel int, live *liveCharacter, sink packetSink, req protocol.PortalRequest) error {
	if handled, err := s.sceneTransfer(ctx, channel, live, sink, req); handled {
		return err
	}
	if req.Revival != nil {
		return s.reviveGameplay(ctx, channel, live, sink, req)
	}
	live.mu.Lock()
	chr := live.session.Character
	live.mu.Unlock()
	m, err := s.gameplay.catalog.Map(chr.MapID)
	if err != nil {
		return err
	}
	if chr.HP <= 0 {
		slog.Info("change_map_while_dead", "character", chr.ID, "map", chr.MapID, "target", req.TargetMap, "portal", req.Name)
		return sink.WritePacket(protocol.EnableActions())
	}
	p, ok := m.Portal(req.Name)
	if !ok || req.TargetMap != -1 || !within(chr.X, chr.Y, p.X, p.Y, 150) {
		return sink.WritePacket(protocol.EnableActions())
	}
	if p.Script != "" {
		return s.runPortalScript(ctx, channel, live, sink, p)
	}
	if (p.Type != 1 && p.Type != 2 && p.Type != 3) || p.TargetMap == 999999999 {
		return sink.WritePacket(protocol.EnableActions())
	}
	to, err := s.gameplay.catalog.Map(p.TargetMap)
	if err != nil {
		return sink.WritePacket(protocol.EnableActions())
	}
	entry, ok := to.Portal(p.TargetName)
	if !ok {
		entry, ok = to.Spawn()
	}
	if !ok {
		return sink.WritePacket(protocol.EnableActions())
	}
	s.cancelNPCScript(live)
	s.closeEconomyDialog(live)
	return s.warpGameplay(ctx, channel, live, sink, to.ID, entry)
}

func (s *Server) warpGameplay(ctx context.Context, channel int, live *liveCharacter, sink packetSink, mapID int, entry content.Portal) error {
	return s.warpGameplayTransition(ctx, channel, live, sink, mapID, entry, nil)
}

func (s *Server) warpGameplayTransition(ctx context.Context, channel int, live *liveCharacter, sink packetSink, mapID int, entry content.Portal, revival *revivalTransition) error {
	return s.warpGameplayConditional(ctx, channel, live, sink, mapID, entry, revival, nil, nil)
}

func (s *Server) warpGameplayConditional(ctx context.Context, channel int, live *liveCharacter, sink packetSink, mapID int, entry content.Portal, revival *revivalTransition, guard func(domain.CharacterSession) bool, beforeSave [][]byte) error {
	live.persistMu.Lock()
	expectedVisit := s.sceneFor(live, sink)
	if expectedVisit == nil {
		live.persistMu.Unlock()
		return domain.ErrStaleOwner
	}
	live.mu.Lock()
	session := live.session
	session.Lease = s.currentLease()
	before := session.Character
	if guard != nil && !guard(session) {
		live.mu.Unlock()
		live.persistMu.Unlock()
		return s.writeScenePackets(expectedVisit, protocol.EnableActions())
	}
	if !s.eventAllowsEntry(session, mapID) {
		live.mu.Unlock()
		live.persistMu.Unlock()
		return s.writeScenePackets(expectedVisit, protocol.EnableActions())
	}
	if revival != nil && (before.HP != 0 || before.MaxHP < 1 || before.MapID != revival.fromMap) {
		live.mu.Unlock()
		live.persistMu.Unlock()
		return s.writeScenePackets(expectedVisit, protocol.UpdateHPMP(before.HP, before.MP))
	}
	live.mu.Unlock()
	if err := s.writeScenePackets(expectedVisit, beforeSave...); err != nil {
		live.persistMu.Unlock()
		return err
	}
	if revival != nil {
		if err := s.clearDeathBuffs(ctx, live, sink, expectedVisit); err != nil {
			live.persistMu.Unlock()
			return s.writeScenePackets(expectedVisit, protocol.UpdateHPMP(before.HP, before.MP))
		}
		// Buff cancellation may have committed the dead vitals and advanced
		// the revision. The return must save that latest fenced aggregate.
		session = s.skillSession(live)
		before = session.Character
	}
	value := before
	if revival != nil {
		// Match the reference's ordinary return policy, bounded by real MaxHP.
		hp := revival.hp
		if hp == 0 {
			hp = 1500
		}
		value.HP = min(value.MaxHP, hp)
	}
	value.MapID = mapID
	value.SpawnPoint = entry.ID
	value.X = entry.X
	value.Y = entry.Y
	saveCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	err := s.repo.SaveCharacter(saveCtx, session, value)
	cancel()
	if err != nil {
		live.persistMu.Unlock()
		if revival != nil {
			return s.writeScenePackets(expectedVisit, protocol.UpdateHPMP(before.HP, before.MP))
		}
		return s.writeScenePackets(expectedVisit, protocol.EnableActions())
	}
	value.Revision++
	snapshot := session
	snapshot.Character = value
	// SQL can commit while a replacement is entering. Do not mutate even this
	// old live aggregate before the exact expected visit is checked under gate.
	err = s.publishGameplayWarp(ctx, channel, live, sink, before, snapshot, revival != nil, expectedVisit, nil, entry.Type == doorEntryType)
	live.persistMu.Unlock()
	return err
}

// Caller retains own persistMu. The CID gate spans all local transition state
// and packets, but no global map/skill registry mutex spans SQL or another lock.
func (s *Server) publishGameplayWarp(ctx context.Context, channel int, live *liveCharacter, sink packetSink, before domain.Character, snapshot domain.CharacterSession, updateVitals bool, expected *sceneVisit, beforeField func() error, chase bool) error {
	if expected == nil || expected.live != live || !sameDropSink(expected.sink, sink) || expected.owner.ChannelID != channel || expected.owner.Character.MapID != before.MapID || !sameSceneOwner(expected.owner, snapshot) {
		return domain.ErrStaleOwner
	}
	unlock, err := s.lockScenePublication(snapshot.CharacterID)
	if err != nil {
		return err
	}
	defer unlock()
	if !s.sceneCurrent(expected) {
		return domain.ErrStaleOwner
	}
	snapshot.Character.PlacePetsAtOwner()
	value := snapshot.Character
	live.mu.Lock()
	invalidateMapEntryQueue(live.mapEntry)
	live.dirty = false
	live.mu.Unlock()
	nextVisit := makeSceneVisit(snapshot, live, sink)
	s.installScene(nextVisit, snapshot)
	if s.maps != nil {
		s.maps.Leave(channel, before.MapID, value.ID, sink)
	}
	if s.drops != nil {
		s.drops.Untrack(channel, before.MapID, value.ID, sink)
	}
	s.merchantLeave(live)
	if s.gameplay != nil {
		s.leaveGameplay(channel, before.MapID, value.ID, sink)
	}
	if beforeField != nil {
		if err = beforeField(); err != nil {
			return err
		}
	}
	if updateVitals {
		if err = sink.WritePacket(protocol.UpdateHPMP(value.HP, value.MP)); err != nil {
			return err
		}
	}
	warp := protocol.WarpMap(value, channel)
	if chase {
		// Mystic Door: the client cannot spawn on a town-portal (tp) index, so
		// SetField carries the exact door position (chase flag, 0x69EDC2).
		warp = protocol.WarpMapAt(value, channel, value.X, value.Y)
	}
	if err = sink.WritePacket(warp); err != nil {
		return err
	}
	if s.maps != nil {
		s.maps.Enter(channel, value.MapID, value, sink)
	}
	if err = showOwnPets(sink, value); err != nil {
		return err
	}
	if s.drops != nil {
		s.drops.Track(channel, value.MapID, value.ID, sink, snapshot)
	}
	s.sendMerchants(channel, value.MapID, sink)
	if err = s.sendInventoryDrops(ctx, snapshot, sink); err != nil {
		return err
	}
	if s.gameplay != nil {
		if err = s.enterGameplay(channel, live, sink); err != nil {
			return err
		}
	}
	s.refreshParty(live)
	if err = s.enterSkills(ctx, channel, live, sink); err != nil {
		return err
	}
	s.publishStatDetail(live, sink, time.Now(), true)
	s.enqueueMapEntry(channel, live, sink)
	s.eventEntered(channel, live, sink)
	return nil
}

func (s *Server) npcGameplay(channel int, live *liveCharacter, sink packetSink, objectID int32) error {
	live.mu.Lock()
	chr := live.session.Character
	live.mu.Unlock()
	g := s.gameplay
	g.mu.Lock()
	r := g.rooms[mapKey{channel: channel, mapID: chr.MapID}]
	var npc content.Life
	var ok bool
	if r != nil {
		npc, ok = r.npcs[objectID]
	}
	g.mu.Unlock()
	if !ok || !npcReach(chr.X, chr.Y, npc.X, npc.Y) {
		return sink.WritePacket(protocol.EnableActions())
	}
	// Explicit Go tutorial text; legacy NPC scripts are a separate coverage item.
	s.npcGMNotice(live, sink, npc.TemplateID, "改顯示預設說明文字(此 NPC 尚未移植)")
	return sink.WritePacket(protocol.NPCDialogue(npc.TemplateID, "歡迎來到楓之谷！使用方向鍵移動、Alt 跳躍、Ctrl 攻擊。沿地圖傳送點前進，可與其他玩家一起探索。", false, false))
}

func (s *Server) monsterMovement(channel int, live *liveCharacter, sink packetSink, m protocol.MonsterMove) error {
	expected := s.sceneFor(live, sink)
	if expected == nil || expected.owner.ChannelID != channel {
		return domain.ErrStaleOwner
	}
	unlock, err := s.lockScenePublication(expected.owner.CharacterID)
	if err != nil {
		return err
	}
	defer unlock()
	if !s.sceneCurrent(expected) {
		return nil
	}
	live.mu.Lock()
	chr := live.session.Character
	live.mu.Unlock()
	g := s.gameplay
	g.mu.Lock()
	r := g.rooms[mapKey{channel: channel, mapID: chr.MapID}]
	if r == nil {
		g.mu.Unlock()
		return nil
	}
	member := r.members[chr.ID]
	if member == nil || member.live != live || !sameDropSink(member.sink, sink) {
		g.mu.Unlock()
		return nil
	}
	mob := r.mobs[m.ObjectID]
	if mob == nil || mob.hp <= 0 || mob.controller != chr.ID {
		g.mu.Unlock()
		return nil
	}
	mp := mob.stats.MP
	switch applyMobMoveLocked(r, mob, m) {
	case mobMoveRejected:
		g.mu.Unlock()
		return sink.WritePacket(protocol.MonsterMoveResponse(m, mp))
	case mobMoveFellOut:
		template := mob.life.TemplateID
		out := g.reseatFallenMobLocked(r, mob)
		g.mu.Unlock()
		slog.Warn("mob_fell_out", "map", chr.MapID, "channel", channel, "mob", m.ObjectID, "template", template, "x", m.X, "y", m.Y, "controller", chr.ID, "reseated", len(out) > 0)
		for _, d := range out {
			_ = d.sink.WritePacket(d.packet)
		}
		return sink.WritePacket(protocol.MonsterMoveResponse(m, mp))
	}
	// Mob skills need their own authoritative resolver; a controller cannot ask
	// peers to execute an arbitrary skill through its movement report.
	m.UseSkill = false
	m.SkillData = 0
	var peers []packetSink
	for id, p := range r.members {
		if id != chr.ID {
			peers = append(peers, p.sink)
		}
	}
	g.mu.Unlock()
	if err := sink.WritePacket(protocol.MonsterMoveResponse(m, mp)); err != nil {
		return err
	}
	packet := protocol.MonsterMovePacket(m)
	for _, p := range peers {
		_ = p.WritePacket(packet)
	}
	return nil
}

type combatRewardRepository interface {
	AwardCombat(context.Context, domain.CharacterSession, string, int64) (domain.Character, error)
}
type combatQuestRepository interface {
	AwardCombatKill(context.Context, domain.CharacterSession, string, int64, int) (domain.Character, error)
}
type combatPositionRepository interface {
	AwardCombatKillAt(context.Context, domain.CharacterSession, string, int64, int, int, int) (domain.Character, error)
}

func (s *Server) meleeGameplay(ctx context.Context, channel int, live *liveCharacter, sink packetSink, a protocol.MeleeAttack) error {
	return s.settledMeleeGameplay(ctx, channel, live, sink, a, domain.CombatNormal)
}
