package world

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

func qaMobWorldFixture(t *testing.T) (*Server, *liveCharacter, *gameplayTestSink, *gmTestRepo) {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"Map.wz/Map/Map1/180000001.img.xml": `<imgdir name="180000001.img"><imgdir name="info"><int name="returnMap" value="180000000"/></imgdir><imgdir name="portal"><imgdir name="0"><string name="pn" value="sp"/><int name="pt" value="0"/><int name="x" value="0"/><int name="y" value="215"/></imgdir></imgdir><imgdir name="foothold"><imgdir name="0"><imgdir name="0"><imgdir name="1"><int name="x1" value="-399"/><int name="y1" value="215"/><int name="x2" value="399"/><int name="y2" value="215"/></imgdir></imgdir></imgdir></imgdir></imgdir>`,
		"Mob.wz/7654321.img.xml":            `<imgdir name="7654321.img"><imgdir name="info"><int name="maxHP" value="97"/><int name="exp" value="13"/><int name="level" value="8"/></imgdir><imgdir name="stand"><canvas name="0"><vector name="lt" x="-12" y="-22"/><vector name="rb" x="12" y="0"/></canvas></imgdir></imgdir>`,
	}
	for path, raw := range files {
		full := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
	}
	catalog, err := content.NewCatalog(root)
	if err != nil {
		t.Fatal(err)
	}
	s, live, sink, repo := gmWorldFixture(t)
	live.session.Character.MapID = domain.QAMobMap
	s.online[live.session.CharacterID] = live.session
	seedBoundScene(t, s, live, sink)
	// Replace the skill membership with the new field identity.
	if err = s.enterSkills(context.Background(), live.session.ChannelID, live, sink); err != nil {
		t.Fatal(err)
	}
	s.gameplay = &gameplaySpace{catalog: catalog, rooms: map[mapKey]*gameplayRoom{}, nextID: 1000000, bootID: strings.Repeat("a", 32)}
	if err = s.enterGameplay(live.session.ChannelID, live, sink); err != nil {
		t.Fatal(err)
	}
	repo.execute = func(req domain.GMCommandRequest) (domain.GMCommandResult, error) {
		return domain.GMCommandResult{Name: req.Name, Args: append([]int64(nil), req.Args...), Character: req.Session.Character, QAMobs: &domain.QAMobAuthorization{RuntimeID: req.RuntimeID, ExpiresAt: time.Now().Add(time.Minute), GrantRevision: 1}}, nil
	}
	return s, live, sink, repo
}

func TestQAMobsNativeCommandsRealWZSpawnAndOwnedClear(t *testing.T) {
	s, live, sink, _ := qaMobWorldFixture(t)
	peerSink := &gameplayTestSink{}
	peer := live.session
	peer.CharacterID = 99
	peer.Character.ID = 99
	peer.SessionID = "qa-observer"
	peerLive := &liveCharacter{session: peer}
	if err := s.enterGameplay(peer.ChannelID, peerLive, peerSink); err != nil {
		t.Fatal(err)
	}
	if _, err := s.handleGMChat(context.Background(), live, sink, "!spawn 7654321 2"); err != nil {
		t.Fatal(err)
	}
	g := s.gameplay
	key := mapKey{live.session.ChannelID, domain.QAMobMap}
	r := g.rooms[key]
	if len(r.mobs) != 2 || sink.count(protocol.SendSpawnMonster) != 2 || sink.count(protocol.SendControlMonster) != 2 {
		t.Fatalf("spawn/control count mismatch: mobs=%d", len(r.mobs))
	}
	for _, m := range r.mobs {
		if m.stats.HP != 97 || m.hp != 97 || m.stats.EXP != 13 || m.life.TemplateID != 7654321 || m.life.Foothold != 1 || m.life.Y != 214 || m.controller != live.session.CharacterID || m.qaOwner == nil {
			t.Fatalf("not actual WZ/runtime mob: %+v", m)
		}
	}
	if peerSink.count(protocol.SendSpawnMonster) != 2 || peerSink.count(protocol.SendControlMonster) != 0 {
		t.Fatal("observer did not receive actual spawn IDs exactly once")
	}
	for _, p := range peerSink.packets {
		if len(p) >= 6 && binary.LittleEndian.Uint16(p) == protocol.SendSpawnMonster {
			id := int32(binary.LittleEndian.Uint32(p[2:6]))
			if r.mobs[id] == nil {
				t.Fatal("observer received invented object", id)
			}
		}
	}
	natural := &gameplayMob{objectID: 7, hp: 5, stats: content.Mob{HP: 5}}
	r.mobs[7] = natural
	if _, err := s.handleGMChat(context.Background(), live, sink, "!clearmobs"); err != nil {
		t.Fatal(err)
	}
	if len(r.mobs) != 1 || r.mobs[7] != natural {
		t.Fatal("clear touched natural mob or retained QA mobs")
	}
	if peerSink.count(protocol.SendKillMonster) != 2 {
		t.Fatal("observer missed QA removals")
	}
	if len(g.qaMobOutcomes) != 2 {
		t.Fatal("runtime outcomes not tracked")
	}
}

func TestQAMobActualConnNeverRespawnsAfterConcurrentExpiry(t *testing.T) {
	s, live, _, _ := qaMobWorldFixture(t)
	server, client, gate := tradeConnPair(t)
	seedBoundScene(t, s, live, server)
	if err := s.enterSkills(context.Background(), live.session.ChannelID, live, server); err != nil {
		t.Fatal(err)
	}
	if err := s.enterGameplay(live.session.ChannelID, live, server); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := s.handleGMChat(context.Background(), live, server, "!spawn 7654321 1"); done <- err }()
	tradeAwait(t, gate.started, "QA spawn did not reach blocked native stream")
	expired := make(chan struct{})
	go func() { s.expireQAMobs(time.Now().Add(2 * time.Minute)); close(expired) }()
	deadline := time.Now().Add(time.Second)
	for {
		s.gameplay.mu.Lock()
		n := len(s.gameplay.rooms[mapKey{live.session.ChannelID, domain.QAMobMap}].mobs)
		s.gameplay.mu.Unlock()
		if n == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("expiry waited for slow socket under room mutex")
		}
		time.Sleep(time.Millisecond)
	}
	close(gate.release)
	spawn, control, stop, remove := -1, -1, -1, -1
	for i := 0; i < 5; i++ {
		p, err := client.ReadPacket()
		if err != nil {
			t.Fatal(err)
		}
		switch binary.LittleEndian.Uint16(p) {
		case protocol.SendSpawnMonster:
			spawn = i
		case protocol.SendControlMonster:
			if p[2] == 0 {
				stop = i
			} else {
				control = i
			}
		case protocol.SendKillMonster:
			remove = i
		}
	}
	if spawn < 0 || control <= spawn || stop <= control || remove != stop+1 {
		t.Fatalf("expiry overtook spawn/control or separated stop/leave: %d/%d/%d/%d", spawn, control, stop, remove)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("spawn flush leaked")
	}
	tradeAwait(t, expired, "expiry flush leaked")
}

type qaMobCombatRepo struct {
	gameplayTestRepo
	apply func(domain.CombatAttackRequest) (domain.CombatAttackResult, error)
}

func (r qaMobCombatRepo) ApplyCombatAttack(_ context.Context, q domain.CombatAttackRequest) (domain.CombatAttackResult, error) {
	return r.apply(q)
}

func TestQAMobsKilledThroughOrdinaryCombatSettlementOnlyOnce(t *testing.T) {
	s, live, sink, _ := qaMobWorldFixture(t)
	live.session.Character.STR = 1000
	live.session.Character.DEX = 100
	live.session.Character.Equipment = []domain.Equipment{{ItemID: 1302000, Slot: -11, WATK: 100}}
	s.online[live.session.CharacterID] = live.session
	if _, err := s.handleGMChat(context.Background(), live, sink, "!spawn 7654321 1"); err != nil {
		t.Fatal(err)
	}
	r := s.gameplay.rooms[mapKey{live.session.ChannelID, domain.QAMobMap}]
	var target *gameplayMob
	for _, m := range r.mobs {
		target = m
	}
	live.session.Character.X, live.session.Character.Y = target.life.X, target.life.Y
	calls := 0
	s.repo = qaMobCombatRepo{apply: func(q domain.CombatAttackRequest) (domain.CombatAttackResult, error) {
		calls++
		if len(q.Targets) != 1 || q.Targets[0].MobID != 7654321 || q.Targets[0].Damage != 97 || !q.Targets[0].Defeated || !strings.Contains(q.Targets[0].SpawnOperationID, s.gameplay.bootID) {
			t.Fatal("bypassed real spawn settlement", q.Targets)
		}
		c := q.Session.Character
		c.EXP += 13
		c.Revision++
		return domain.CombatAttackResult{Characters: []domain.Character{c}}, nil
	}}
	a := protocol.MeleeAttack{Tick: 1, Display: 35, Speed: 6, Targets: []protocol.MeleeTarget{{ObjectID: target.objectID, Damage: 97}}}
	if err := s.meleeGameplay(context.Background(), live.session.ChannelID, live, sink, a); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || live.session.Character.EXP != 13 || target.hp != 0 || sink.count(protocol.SendKillMonster) != 1 {
		t.Fatalf("combat did not settle actual QA mob: calls%d exp%d hp%d", calls, live.session.Character.EXP, target.hp)
	}
	if err := s.meleeGameplay(context.Background(), live.session.ChannelID, live, sink, a); err != nil {
		t.Fatal(err)
	}
	before := s.gameplay.nextID
	s.gameplay.respawn(time.Now().Add(time.Minute))
	if calls != 1 || len(r.mobs) != 0 || s.gameplay.nextID != before {
		t.Fatal("QA kill replayed or respawned")
	}
}

func TestQAMobsReplayConcurrentCapacityAndForeignClear(t *testing.T) {
	s, live, sink, _ := qaMobWorldFixture(t)
	g := s.gameplay
	q := domain.GMCommandRequest{Session: live.session, Name: "spawn", OperationID: "qa-runtime-replay", Args: []int64{7654321, 6}, RuntimeID: g.bootID}
	key := mapKey{q.Session.ChannelID, domain.QAMobMap}
	r := g.rooms[key]
	mob, _ := g.catalog.Mob(7654321)
	body, _ := g.catalog.MobBodyBounds(7654321)
	lives, _ := qaMobPositions(r.definition, body, 6, 0, 214)
	for i := range lives {
		lives[i].TemplateID = mob.ID
	}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			g.mu.Lock()
			defer g.mu.Unlock()
			ids, _, err := g.applyQAMobsLocked(key, q, time.Now().Add(time.Minute), live, sink, mob, lives, time.Now())
			if err != nil || len(ids) != 6 {
				t.Errorf("replay %v %v", ids, err)
			}
		}()
	}
	wg.Wait()
	if len(r.mobs) != 6 || g.nextID != 1000006 {
		t.Fatal("duplicate runtime objects")
	}
	q.Args = []int64{7654321, 5}
	if _, _, err := g.applyQAMobsLocked(key, q, time.Now().Add(time.Minute), live, sink, mob, lives, time.Now()); err == nil {
		t.Fatal("replay changed quantity")
	}
	q.OperationID += "-new"
	q.Args = []int64{7654321, 7}
	if _, _, err := g.applyQAMobsLocked(key, q, time.Now().Add(time.Minute), live, sink, mob, append(lives, lives[0]), time.Now()); err == nil || len(r.mobs) != 6 {
		t.Fatal("owner limit bypassed")
	}
	other := q.Session
	other.CharacterID = 99
	other.SessionID = "foreign"
	otherLive := &liveCharacter{session: other}
	otherSink := &gameplayTestSink{}
	r.members[99] = &gameplayMember{live: otherLive, sink: otherSink, sessionID: other.SessionID, generation: other.Generation, worldID: other.Lease.WorldID}
	clear := domain.GMCommandRequest{Session: other, Name: "clearmobs", OperationID: "qa-foreign-clear"}
	ids, _, err := g.applyQAMobsLocked(key, clear, time.Now().Add(time.Minute), otherLive, otherSink, content.Mob{}, nil, time.Now())
	if err != nil || len(ids) != 0 || len(r.mobs) != 6 {
		t.Fatal("foreign owner cleared batch", err)
	}
}

func TestQAMobsClearWaitsPendingSettlementExpiryAndNoRespawn(t *testing.T) {
	s, live, sink, _ := qaMobWorldFixture(t)
	if _, err := s.handleGMChat(context.Background(), live, sink, "!spawn 7654321 2"); err != nil {
		t.Fatal(err)
	}
	g := s.gameplay
	key := mapKey{live.session.ChannelID, domain.QAMobMap}
	r := g.rooms[key]
	var first *gameplayMob
	for _, m := range r.mobs {
		first = m
		break
	}
	first.claiming = true
	first.reservation = &combatTicket{}
	if _, err := s.handleGMChat(context.Background(), live, sink, "!clearmobs"); err != nil || len(r.mobs) != 2 {
		t.Fatal("partial clear during settlement", err)
	}
	s.expireQAMobs(time.Now().Add(2 * time.Minute))
	if len(r.mobs) != 1 || r.mobs[first.objectID] != first || !first.qaOwner.retired || first.hp != 97 {
		t.Fatal("expiry destroyed pending ticket or changed HP")
	}
	first.claiming = false
	first.reservation = nil
	s.expireQAMobs(time.Now().Add(2 * time.Minute))
	if len(r.mobs) != 0 {
		t.Fatal("expired pending object retained")
	}
	if _, err := s.handleGMChat(context.Background(), live, sink, "!spawn 7654321 1"); err != nil {
		t.Fatal(err)
	}
	for _, m := range r.mobs {
		m.hp = 0
		m.respawnAt = time.Now().Add(-time.Minute)
	}
	before := g.nextID
	g.respawn(time.Now())
	if len(r.mobs) != 0 || g.nextID != before {
		t.Fatal("QA death respawned")
	}
}

func TestQAMobsDepartedGenerationAndReplacementIsolation(t *testing.T) {
	s, live, sink, _ := qaMobWorldFixture(t)
	if _, err := s.handleGMChat(context.Background(), live, sink, "!spawn 7654321 1"); err != nil {
		t.Fatal(err)
	}
	g := s.gameplay
	key := mapKey{live.session.ChannelID, domain.QAMobMap}
	r := g.rooms[key]
	old := r.members[live.session.CharacterID]
	replacement := &gameplayTestSink{}
	r.members[live.session.CharacterID] = &gameplayMember{live: live, sink: replacement, sessionID: "new", generation: old.generation + 1, worldID: old.worldID}
	s.leaveGameplay(key.channel, key.mapID, live.session.CharacterID, sink)
	if r.members[live.session.CharacterID] == nil || len(r.mobs) != 1 {
		t.Fatal("old cleanup removed new member")
	}
	s.expireQAMobs(time.Now())
	if len(r.mobs) != 0 || r.members[live.session.CharacterID] == nil {
		t.Fatal("stale generation batch not retired safely")
	}
}

func TestQAMobsAuthorizationAndSceneFailuresNeverSpawn(t *testing.T) {
	for _, kind := range []string{"denied", "expired", "old-runtime", "old-visit", "unknown", "bad-count", "wrong-map", "pending"} {
		t.Run(kind, func(t *testing.T) {
			s, live, sink, repo := qaMobWorldFixture(t)
			original := repo.execute
			calls := 0
			repo.execute = func(q domain.GMCommandRequest) (domain.GMCommandResult, error) {
				calls++
				r, _ := original(q)
				switch kind {
				case "denied":
					return r, domain.ErrForbidden
				case "expired":
					r.QAMobs.ExpiresAt = time.Now().Add(-time.Second)
				case "old-runtime":
					r.QAMobs.RuntimeID = strings.Repeat("b", 32)
				case "old-visit":
					s.scenes[q.Session.CharacterID] = makeSceneVisit(q.Session, live, sink)
				case "unknown":
					return r, errors.New("unknown commit")
				}
				return r, nil
			}
			repo.resolve = func(domain.GMCommandRequest) (domain.GMCommandResult, bool, error) {
				return domain.GMCommandResult{}, false, errors.New("still unknown")
			}
			cmd := "!spawn 7654321 1"
			if kind == "bad-count" {
				cmd = "!spawn 7654321 13"
			}
			if kind == "wrong-map" {
				live.session.Character.MapID = 104000000
			}
			if kind == "pending" {
				live.mapEntry = &mapEntryQueue{pending: &mapEntryRequest{}}
			}
			_, _ = s.handleGMChat(context.Background(), live, sink, cmd)
			for _, r := range s.gameplay.rooms {
				if len(r.mobs) != 0 {
					t.Fatal("invalid command spawned")
				}
			}
			if (kind == "bad-count" || kind == "pending") && calls != 0 {
				t.Fatal("preflight reached store")
			}
		})
	}
}

func TestQAMobFootholdGeometryAndStrictGrammar(t *testing.T) {
	m := content.Map{ID: domain.QAMobMap, Footholds: []content.Foothold{{ID: 1, X1: -399, Y1: 215, X2: 399, Y2: 215}}}
	b := content.MobBodyBounds{Envelope: content.MobBodyRect{Left: -12, Top: -22, Right: 12, Bottom: 0}, FrameCount: 1}
	lives, err := qaMobPositions(m, b, 12, 9999, 214)
	if err != nil || len(lives) != 12 {
		t.Fatal(lives, err)
	}
	for _, l := range lives {
		if l.X+b.Envelope.Left <= -399 || l.X+b.Envelope.Right >= 399 || l.Y != 214 {
			t.Fatal("outside real foothold", l)
		}
	}
	before := m
	m.Life = []content.Life{{Kind: "m"}}
	if _, err = qaMobPositions(m, b, 1, 0, 214); err == nil {
		t.Fatal("natural monster field accepted")
	}
	m = before
	b.Envelope.Right = 5000
	if _, err = qaMobPositions(m, b, 1, 0, 214); err == nil {
		t.Fatal("oversized body accepted")
	}
	for _, cmd := range []string{"!spawn 1", "!spawn 1 2 3 4", "!spawn -1 1", "!clearmobs 1"} {
		if n, _, _ := parseGMChat(cmd); n != "" {
			t.Fatal("accepted", cmd)
		}
	}
	if n, a, _ := parseGMChat("!spawn 7654321 12"); n != "spawn" || !reflect.DeepEqual(a, []int64{7654321, 12}) {
		t.Fatal(n, a)
	}
}

func TestQAMobExplicitCenterGrammarAndAuthorityBounds(t *testing.T) {
	for _, x := range []int64{-32000, -244, 0, 131, 32000} {
		text := "!spawn 7654321 2 " + strconv.FormatInt(x, 10)
		name, args, _ := parseGMChat(text)
		if name != "spawn" || !reflect.DeepEqual(args, []int64{7654321, 2, x}) || !domain.ValidQAMobCommand(name, args, domain.QAMobMap) {
			t.Fatal("explicit center rejected", text, name, args)
		}
	}
	for _, text := range []string{"!spawn 1 2 -", "!spawn 1 2 +1", "!spawn 1 2 --1", "!spawn 1 2 1.0", "!spawn 1 2 0x10", "!spawn 1 2 1e2", "!spawn 1 2 -999999999999999999999", "!spawn 1 -2 3", "!skill 1 -2", "!warp -1"} {
		if name, _, _ := parseGMChat(text); name != "" {
			t.Fatal("invalid grammar accepted", text)
		}
	}
	for _, args := range [][]int64{{1}, {1, 2, -32001}, {1, 2, 32001}, {1, 2, 0, 0}, {0, 2, 0}, {1, 13, 0}, {1, 0, 0}} {
		if domain.ValidQAMobCommand("spawn", args, domain.QAMobMap) {
			t.Fatal("invalid spawn authority", args)
		}
	}
	if domain.ValidQAMobCommand("spawn", []int64{1, 1, 0}, 100000000) {
		t.Fatal("explicit center widened map permission")
	}
}

func TestQAMobExplicitCenterExactGroupAndNoClamp(t *testing.T) {
	m := content.Map{ID: domain.QAMobMap, Footholds: []content.Foothold{{ID: 1, X1: -399, Y1: 215, X2: 399, Y2: 215}}}
	b := content.MobBodyBounds{Envelope: content.MobBodyRect{Left: -70, Top: -140, Right: 70, Bottom: 0}, FrameCount: 1}
	for _, c := range []struct{ n, x int }{{1, -244}, {1, 244}, {2, -131}, {3, 131}, {12, -145}, {12, 145}, {1, -321}, {1, 321}} {
		lives, err := qaMobPositionsAtCenter(m, b, c.n, c.x, 179)
		if err != nil || len(lives) != c.n {
			t.Fatal(c, lives, err)
		}
		for i, life := range lives {
			if life.X != c.x-16*(c.n-1)+32*i || life.Y != 214 || life.Foothold != 1 || life.X-70 < -391 || life.X+70 > 391 {
				t.Fatal("shifted group or fabricated floor", c, life)
			}
		}
	}
	for _, c := range []struct{ n, x int }{{1, -322}, {1, 322}, {12, -146}, {12, 146}, {1, -32001}, {1, 32001}} {
		if lives, err := qaMobPositionsAtCenter(m, b, c.n, c.x, 214); err == nil || lives != nil {
			t.Fatal("explicit center clamped or partially placed", c, lives, err)
		}
	}
	// Original two-argument placement still clamps the near-actor group to fit.
	legacy, err := qaMobPositions(m, b, 12, 9999, 214)
	if err != nil || legacy[0].X != -31 || legacy[11].X != 321 {
		t.Fatal("legacy placement changed", legacy, err)
	}
	m.Footholds = []content.Foothold{{ID: 1, X1: -399, X2: -50, Y1: 215, Y2: 215}, {ID: 2, X1: 50, X2: 399, Y1: 215, Y2: 215}}
	if lives, err := qaMobPositionsAtCenter(m, b, 1, 0, 214); err == nil || lives != nil {
		t.Fatal("center in real floor gap accepted", lives, err)
	}
}

func TestQAMobExplicitCenterNativePublicationKeepsActorAndRejectsWholeBatch(t *testing.T) {
	s, live, sink, repo := qaMobWorldFixture(t)
	live.session.Character.X = 350
	s.online[live.session.CharacterID] = live.session
	var stored []int64
	previous := repo.execute
	repo.execute = func(q domain.GMCommandRequest) (domain.GMCommandResult, error) {
		stored = append([]int64(nil), q.Args...)
		return previous(q)
	}
	if _, err := s.handleGMChat(context.Background(), live, sink, "!spawn 7654321 3 -131"); err != nil {
		t.Fatal(err)
	}
	r := s.gameplay.rooms[mapKey{live.session.ChannelID, domain.QAMobMap}]
	want := map[int]bool{-163: true, -131: true, -99: true}
	for _, mob := range r.mobs {
		if !want[mob.life.X] || mob.life.Y != 214 || mob.hp != 97 || mob.qaOwner.member != r.members[live.session.CharacterID] {
			t.Fatal("explicit spawn not exact/owned/real", mob)
		}
		delete(want, mob.life.X)
	}
	if len(want) != 0 || live.session.Character.X != 350 || !reflect.DeepEqual(stored, []int64{7654321, 3, -131}) || sink.count(protocol.SendSpawnMonster) != 3 {
		t.Fatal("actor moved or center not audited", want, stored)
	}
	next, outcomes, packets := s.gameplay.nextID, len(s.gameplay.qaMobOutcomes), sink.count(protocol.SendSpawnMonster)
	if _, err := s.handleGMChat(context.Background(), live, sink, "!spawn 7654321 2 380"); err != nil {
		t.Fatal(err)
	}
	if len(r.mobs) != 3 || s.gameplay.nextID != next || len(s.gameplay.qaMobOutcomes) != outcomes || sink.count(protocol.SendSpawnMonster) != packets {
		t.Fatal("clamped/partial explicit batch published")
	}
}

func TestQAMobExplicitCenterRuntimeReplayBindsModeCenterAndOwner(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(strconv.FormatBool(explicit), func(t *testing.T) {
			s, live, sink, _ := qaMobWorldFixture(t)
			g := s.gameplay
			q := domain.GMCommandRequest{Session: live.session, Name: "spawn", OperationID: "same-key-center", Args: []int64{7654321, 2}, RuntimeID: g.bootID}
			if explicit {
				q.Args = append(q.Args, 0)
			}
			key := mapKey{q.Session.ChannelID, domain.QAMobMap}
			mob, _ := g.catalog.Mob(7654321)
			body, _ := g.catalog.MobBodyBounds(7654321)
			lives, _ := qaMobPositionsAtCenter(g.rooms[key].definition, body, 2, 0, 214)
			now := time.Now()
			ids, _, err := g.applyQAMobsLocked(key, q, now.Add(time.Minute), live, sink, mob, lives, now)
			if err != nil {
				t.Fatal(err)
			}
			replayed, deliveries, err := g.applyQAMobsLocked(key, q, now.Add(time.Minute), live, sink, mob, lives, now)
			if err != nil || !reflect.DeepEqual(ids, replayed) || len(deliveries) != 0 {
				t.Fatal("replay respawned", replayed, err)
			}
			for _, args := range [][]int64{{7654321, 2, 1}, {7654321, 2, 0}, {7654321, 2}} {
				if reflect.DeepEqual(args, q.Args) {
					continue
				}
				changed := q
				changed.Args = args
				if _, _, err = g.applyQAMobsLocked(key, changed, now.Add(time.Minute), live, sink, mob, lives, now); !errors.Is(err, domain.ErrForbidden) {
					t.Fatal("same key changed center/mode", args, err)
				}
			}
			changed := q
			changed.Session.SessionID = "other-owner"
			if _, _, err = g.applyQAMobsLocked(key, changed, now.Add(time.Minute), live, sink, mob, lives, now); !errors.Is(err, domain.ErrStaleOwner) {
				t.Fatal("other owner reused receipt", err)
			}
			if len(g.rooms[key].mobs) != 2 || g.nextID != 1000002 {
				t.Fatal("bad replay mutated runtime")
			}
		})
	}
}

func TestQAMobTwelveOnExactOriginalMapSegmentsNeverCrossGaps(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "qa", "180000001.img.xml"))
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(raw)
	if hex.EncodeToString(hash[:]) != "ab81efaa510975e2ade6e003b38c19f9657a09aabb32d09253829eb7a9119417" {
		t.Fatal("original WZ geometry fixture changed")
	}
	root := t.TempDir()
	dir := filepath.Join(root, "Map.wz", "Map", "Map1")
	if err = os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "180000001.img.xml"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	catalog, err := content.NewCatalog(root)
	if err != nil {
		t.Fatal(err)
	}
	m, err := catalog.Map(domain.QAMobMap)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Life) != 0 || len(m.Footholds) != 39 {
		t.Fatalf("unexpected original field: %d life/%d footholds", len(m.Life), len(m.Footholds))
	}
	b := content.MobBodyBounds{Envelope: content.MobBodyRect{Left: -12, Top: -22, Right: 12, Bottom: 0}, FrameCount: 1}
	lives, err := qaMobPositions(m, b, 12, -235, 179)
	if err != nil || len(lives) != 12 {
		t.Fatal("twelve mobs rejected on real segmented floor", err)
	}
	seen := map[int]bool{}
	for _, l := range lives {
		if l.Y != 214 || l.RX0 >= l.RX1 {
			t.Fatal("wrong layer/patrol", l)
		}
		found := false
		for _, fh := range m.Footholds {
			if fh.ID == l.Foothold && fh.Y1 == 215 && fh.Y2 == 215 && l.X >= min(fh.X1, fh.X2) && l.X <= max(fh.X1, fh.X2) {
				found = true
				seen[fh.ID] = true
			}
		}
		if !found {
			t.Fatal("spawn bound to another segment", l)
		}
	}
	if len(seen) < 4 {
		t.Fatal("fixture did not exercise multiple actual FH IDs", seen)
	}
	// New explicit mode also resolves every center to its real segment. Use
	// the original safe boss's full horizontal envelope, not only its center.
	bossBody := content.MobBodyBounds{Envelope: content.MobBodyRect{Left: -70, Top: -140, Right: 70, Bottom: 0}, FrameCount: 1}
	for _, c := range []struct{ n, x int }{{1, -244}, {1, 244}, {3, -131}, {12, 0}} {
		placed, e := qaMobPositionsAtCenter(m, bossBody, c.n, c.x, 179)
		if e != nil || len(placed) != c.n {
			t.Fatal("explicit group rejected on original WZ segments", c, e)
		}
		for i, life := range placed {
			found := false
			for _, fh := range m.Footholds {
				if fh.ID == life.Foothold && fh.Y1 == 215 && fh.Y2 == 215 && life.X >= min(fh.X1, fh.X2) && life.X <= max(fh.X1, fh.X2) {
					found = true
				}
			}
			if !found || life.X != c.x-16*(c.n-1)+32*i || life.Y != 214 || life.X-70 < -391 || life.X+70 > 391 {
				t.Fatal("explicit group drifted from original body/floor", c, life)
			}
		}
	}
	m.Footholds = []content.Foothold{{ID: 1, X1: -400, X2: -50, Y1: 215, Y2: 215}, {ID: 2, X1: 50, X2: 400, Y1: 215, Y2: 215}}
	if _, err = qaMobPositions(m, b, 12, 0, 214); err == nil {
		t.Fatal("joined separated platforms across a real gap")
	}
}

func TestQAMobDelayedCleanupNeverTargetsSameSinkReplacementVisit(t *testing.T) {
	for _, newGeneration := range []bool{false, true} {
		t.Run(strconv.FormatBool(newGeneration), func(t *testing.T) {
			s, live, sink, _ := qaMobWorldFixture(t)
			if _, err := s.handleGMChat(context.Background(), live, sink, "!spawn 7654321 1"); err != nil {
				t.Fatal(err)
			}
			out := s.gameplay.collectExpiredQAMobs(time.Now().Add(2 * time.Minute))
			if len(out) != 1 || out[0].visit == nil {
				t.Fatal("cleanup did not pin exact original visit")
			}
			before := sink.count(protocol.SendKillMonster)
			beforeControl := sink.count(protocol.SendControlMonster)
			if newGeneration {
				live.session.Generation++
				live.session.SessionID = "new-generation"
			}
			s.online[live.session.CharacterID] = live.session
			seedBoundScene(t, s, live, sink) // Same map AND same native stream, a new visit.
			if err := s.enterGameplay(live.session.ChannelID, live, sink); err != nil {
				t.Fatal(err)
			}
			s.flushQAMobCleanup(out)
			if sink.count(protocol.SendKillMonster) != before || sink.count(protocol.SendControlMonster) != beforeControl {
				t.Fatal("old control revocation/removal crossed exact same-map/same-sink visit")
			}
		})
	}
}

func TestQAMobCleanupKeepsSceneGateThroughSocketAndNeverRoomMutex(t *testing.T) {
	s, live, sink, _ := qaMobWorldFixture(t)
	if _, err := s.handleGMChat(context.Background(), live, sink, "!spawn 7654321 1"); err != nil {
		t.Fatal(err)
	}
	blocked := &sceneBlockingSink{entered: make(chan struct{}), release: make(chan struct{})}
	seedBoundScene(t, s, live, blocked)
	key := mapKey{live.session.ChannelID, domain.QAMobMap}
	m := s.gameplay.rooms[key].members[live.session.CharacterID]
	m.sink = blocked
	m.visit = s.sceneFor(live, blocked)
	out := s.gameplay.collectExpiredQAMobs(time.Now().Add(2 * time.Minute))
	done := make(chan struct{})
	go func() { s.flushQAMobCleanup(out); close(done) }()
	tradeAwait(t, blocked.entered, "cleanup did not reach blocked writer")
	// The short room mutex remains available while this recipient's scene
	// cannot advance to SetField until the old removal flush completes.
	roomReady := make(chan struct{})
	go func() { s.gameplay.mu.Lock(); s.gameplay.mu.Unlock(); close(roomReady) }()
	tradeAwait(t, roomReady, "cleanup retained room mutex over socket")
	newVisit := make(chan struct{})
	go func() { unlock, _ := s.lockScenePublication(live.session.CharacterID); defer unlock(); close(newVisit) }()
	select {
	case <-newVisit:
		t.Fatal("new visit overtook queued removal")
	case <-time.After(25 * time.Millisecond):
	}
	close(blocked.release)
	tradeAwait(t, done, "cleanup waiter leaked")
	tradeAwait(t, newVisit, "scene gate waiter leaked")
	if blocked.count(protocol.SendKillMonster) != 1 {
		t.Fatal("cleanup was not delivered exactly once")
	}
}

func TestQAMobConcurrentDeparturesNeverAcquirePeerSceneGates(t *testing.T) {
	s, live, sink, _ := qaMobWorldFixture(t)
	if _, err := s.handleGMChat(context.Background(), live, sink, "!spawn 7654321 1"); err != nil {
		t.Fatal(err)
	}
	peer := live.session
	peer.CharacterID = 99
	peer.Character.ID = 99
	peer.SessionID = "other-owner"
	other := &liveCharacter{session: peer}
	otherSink := &gameplayTestSink{}
	s.online[peer.CharacterID] = peer
	seedBoundScene(t, s, other, otherSink)
	if err := s.enterGameplay(peer.ChannelID, other, otherSink); err != nil {
		t.Fatal(err)
	}
	key := mapKey{peer.ChannelID, domain.QAMobMap}
	r := s.gameplay.rooms[key]
	member := r.members[99]
	r.mobs[8] = &gameplayMob{objectID: 8, hp: 97, controller: 99, qaOwner: &qaMobOwner{owner: peer, member: member, expires: time.Now().Add(time.Minute)}}
	unlockA, _ := s.lockScenePublication(live.session.CharacterID)
	unlockB, _ := s.lockScenePublication(peer.CharacterID)
	doneA, doneB := make(chan struct{}), make(chan struct{})
	go func() { s.leaveGameplay(key.channel, key.mapID, live.session.CharacterID, sink); close(doneA) }()
	go func() { s.leaveGameplay(key.channel, key.mapID, peer.CharacterID, otherSink); close(doneB) }()
	tradeAwait(t, doneA, "departure A waited for peer B gate")
	tradeAwait(t, doneB, "departure B waited for peer A gate")
	unlockA()
	unlockB()
	s.expireQAMobs(time.Now())
	if len(r.mobs) != 0 {
		t.Fatal("retired mobs retained after both departures")
	}
}

// The controller is a room assignment, not necessarily the QA command owner.
// Each removal path must withdraw only the current controller's native object.
func TestQAMobRemovalRevokesCurrentControllerBeforeReasonZero(t *testing.T) {
	for _, path := range []string{"explicit", "expiry", "ownerleave"} {
		for _, controller := range []string{"owner", "peer", "none", "missing"} {
			t.Run(path+"/"+controller, func(t *testing.T) {
				s, live, sink, _ := qaMobWorldFixture(t)
				peer := live.session
				peer.CharacterID, peer.Character.ID, peer.SessionID = 99, 99, "controller-peer"
				peerLive, peerSink := &liveCharacter{session: peer}, &gameplayTestSink{}
				s.online[99] = peer
				seedBoundScene(t, s, peerLive, peerSink)
				if err := s.enterGameplay(peer.ChannelID, peerLive, peerSink); err != nil {
					t.Fatal(err)
				}
				if _, err := s.handleGMChat(context.Background(), live, sink, "!spawn 7654321 1"); err != nil {
					t.Fatal(err)
				}
				key := mapKey{live.session.ChannelID, domain.QAMobMap}
				r := s.gameplay.rooms[key]
				var mob *gameplayMob
				for _, v := range r.mobs {
					mob = v
				}
				switch controller {
				case "peer":
					mob.controller = 99
				case "none":
					mob.controller = 0
				case "missing":
					mob.controller = 123456
				}
				ownerStart, peerStart := packetCount(sink), packetCount(peerSink)
				switch path {
				case "explicit":
					if _, err := s.handleGMChat(context.Background(), live, sink, "!clearmobs"); err != nil {
						t.Fatal(err)
					}
				case "expiry":
					s.expireQAMobs(time.Now().Add(2 * time.Minute))
				case "ownerleave":
					s.leaveGameplay(key.channel, key.mapID, live.session.CharacterID, sink)
					s.expireQAMobs(time.Now())
				}
				if len(r.mobs) != 0 || mob.hp != 97 {
					t.Fatal("cleanup changed damage or retained QA object")
				}
				for _, recipient := range []struct {
					id    int64
					sink  *gameplayTestSink
					start int
				}{{live.session.CharacterID, sink, ownerStart}, {99, peerSink, peerStart}} {
					recipient.sink.mu.Lock()
					packets := append([][]byte(nil), recipient.sink.packets[recipient.start:]...)
					recipient.sink.mu.Unlock()
					var relevant [][]byte
					for _, packet := range packets {
						op := binary.LittleEndian.Uint16(packet)
						if op == protocol.SendControlMonster || op == protocol.SendKillMonster {
							relevant = append(relevant, packet)
						}
					}
					if path == "ownerleave" && recipient.id == live.session.CharacterID {
						if len(relevant) != 0 {
							t.Fatal("departed owner received old-field cleanup")
						}
						continue
					}
					wantStop := mob.controller != 0 && recipient.id == mob.controller
					if wantStop {
						if len(relevant) != 2 || !reflect.DeepEqual(relevant[0], protocol.StopMonsterControl(mob.objectID)) {
							t.Fatalf("controller missing exact stop first: %x", relevant)
						}
					} else if len(relevant) != 1 {
						t.Fatalf("noncontroller received control change: %x", relevant)
					}
					if !reflect.DeepEqual(relevant[len(relevant)-1], protocol.RemoveMonster(mob.objectID)) {
						t.Fatalf("not exact reason0 leave: %x", relevant)
					}
				}
			})
		}
	}
}

type qaMobStopFailureSink struct{ packets [][]byte }

func (s *qaMobStopFailureSink) WritePacket(p []byte) error {
	s.packets = append(s.packets, append([]byte(nil), p...))
	return errors.New("stop write failed")
}
func TestQAMobRemovalStopsAfterRevocationWriteFailure(t *testing.T) {
	sink := &qaMobStopFailureSink{}
	mob := &gameplayMob{objectID: 123, controller: 7}
	if err := writeQAMobPackets(sink, qaMobRemovalPackets(mob, 7)); err == nil || len(sink.packets) != 1 || !reflect.DeepEqual(sink.packets[0], protocol.StopMonsterControl(123)) {
		t.Fatal("failed stop fell through to leave", err, sink.packets)
	}
}

func TestQAMobsPharaohMapSpawnAndClear(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"Map.wz/Map/Map9/926010200.img.xml": `<imgdir name="926010200.img"><imgdir name="info"><int name="town" value="0"/><int name="returnMap" value="926010000"/><int name="forcedReturn" value="926010000"/></imgdir><imgdir name="foothold"><imgdir name="1"><imgdir name="1"><imgdir name="1"><int name="x1" value="-400"/><int name="y1" value="215"/><int name="x2" value="400"/><int name="y2" value="215"/><int name="prev" value="0"/><int name="next" value="0"/></imgdir></imgdir></imgdir></imgdir><imgdir name="portal"><imgdir name="0"><string name="pn" value="sp"/><int name="pt" value="0"/><int name="x" value="0"/><int name="y" value="215"/><int name="tm" value="999999999"/><string name="tn" value=""/></imgdir></imgdir></imgdir>`,
		"Mob.wz/7654321.img.xml":            `<imgdir name="7654321.img"><imgdir name="info"><int name="level" value="10"/><int name="maxHP" value="1000"/><int name="exp" value="15"/><int name="bodyAttack" value="1"/><int name="PADamage" value="20"/></imgdir><imgdir name="stand"><canvas name="0" width="40" height="50"><vector name="origin" x="20" y="50"/><vector name="lt" x="-20" y="-50"/><vector name="rb" x="20" y="0"/></canvas></imgdir></imgdir>`,
	}
	for path, raw := range files {
		full := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
	}
	catalog, err := content.NewCatalog(root)
	if err != nil {
		t.Fatal(err)
	}
	s, live, sink, repo := gmWorldFixture(t)
	live.session.Character.MapID = domain.QAPharaohMap
	live.session.Character.X = 0
	live.session.Character.Y = 215
	s.online[live.session.CharacterID] = live.session
	seedBoundScene(t, s, live, sink)
	if err = s.enterSkills(context.Background(), live.session.ChannelID, live, sink); err != nil {
		t.Fatal(err)
	}
	s.gameplay = &gameplaySpace{catalog: catalog, rooms: map[mapKey]*gameplayRoom{}, nextID: 1000000, bootID: strings.Repeat("a", 32)}
	if err = s.enterGameplay(live.session.ChannelID, live, sink); err != nil {
		t.Fatal(err)
	}
	repo.execute = func(req domain.GMCommandRequest) (domain.GMCommandResult, error) {
		return domain.GMCommandResult{
			Name:      req.Name,
			Args:      append([]int64(nil), req.Args...),
			Character: req.Session.Character,
			QAMobs:    &domain.QAMobAuthorization{RuntimeID: req.RuntimeID, ExpiresAt: time.Now().Add(time.Minute), GrantRevision: 1},
		}, nil
	}
	if _, err := s.handleGMChat(context.Background(), live, sink, "!spawn 7654321 2"); err != nil {
		t.Fatalf("pharaoh !spawn failed: %v", err)
	}
	key := mapKey{live.session.ChannelID, domain.QAPharaohMap}
	if got := len(s.gameplay.rooms[key].mobs); got != 2 {
		t.Fatalf("pharaoh room mobs = %d, want 2", got)
	}
	if _, err := s.handleGMChat(context.Background(), live, sink, "!clearmobs"); err != nil {
		t.Fatalf("pharaoh !clearmobs failed: %v", err)
	}
	if got := len(s.gameplay.rooms[key].mobs); got != 0 {
		t.Fatalf("pharaoh room mobs after clear = %d, want 0", got)
	}
}
