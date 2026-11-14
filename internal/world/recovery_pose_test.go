package world

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRecoveryInitialNativeLandingPairUsesRealQAFloor(t *testing.T) {
	// This is the complete original movement frame retained in the B4917
	// trace. Its final two command0 fragments both end at (-235,215).
	raw, err := hex.DecodeString("47000103f34aad89cd221b15ffa90000000000030015ffd5000000a40100000000000006d2000015ffd700000000000000000000000604000015ffd700000000000500000000000428011100000000000000000015ffa90015ffd700")
	if err != nil {
		t.Fatal(err)
	}
	mapBytes, err := os.ReadFile(filepath.Join("..", "..", "..", "MapleStory_Legacy_Server", "wz", "Map.wz", "Map", "Map1", "180000001.img.xml"))
	if err != nil {
		t.Skip("original QA map WZ unavailable:", err)
	}
	sink := &gameplayTestSink{}
	_, live, _ := recoveryWorldFixture(t, sink)
	mapDir := filepath.Join(os.Getenv("MAPLEGO_WZ_ROOT"), "Map.wz", "Map", "Map1")
	if err := os.MkdirAll(mapDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mapDir, "180000001.img.xml"), mapBytes, 0644); err != nil {
		t.Fatal(err)
	}
	c := &live.session.Character
	c.MapID, c.X, c.Y, c.Job, c.Level = 180000001, -235, 169, 0, 1
	c.HP, c.MP, c.MaxHP, c.MaxMP, c.BaseMaxHP, c.BaseMaxMP = 40, 5, 50, 5, 50, 5
	c.Equipment = nil
	s := &Server{skills: newSkillSpace(), lease: live.session.Lease}
	seedBoundScene(t, s, live, sink)
	if err := s.enterSkills(context.Background(), 1, live, sink); err != nil {
		t.Fatal(err)
	}
	if err := s.moveScene(live, sink, protocol.NewReader(raw[2:])); err != nil {
		t.Fatal(err)
	}
	m := s.skills.members[1]
	if !m.recoveryPose.allowsHP(s.sceneFor(live, sink), live.session.Character) || m.recoveryPose.previousY != 215 {
		t.Fatal("native intra-packet landing did not establish bounded pose", m.recoveryPose)
	}
	q := recoveryCommand(protocol.RecvRegeneration, 454718397, 10, 0)
	landingAt := m.regeneration[0][0].at
	if landingAt.IsZero() || landingAt.Before(m.lastRegen) || m.regeneration[1][0].at != landingAt {
		t.Fatal("initial pair reused pre-landing time or mixed HP source clocks")
	}
	if err := s.regenerateAt(context.Background(), live, sink, q, landingAt.Add(10*time.Second-time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if c.HP != 40 || m.regeneration[0][0].seen {
		t.Fatal("landing bypassed first recovery interval")
	}
	if err := s.regenerateAt(context.Background(), live, sink, q, landingAt.Add(10*time.Second)); err != nil {
		t.Fatal(err)
	}
	if c.HP != 50 || c.MP != 5 || !live.dirty || !m.regeneration[0][0].seen {
		t.Fatal("ordinary request lost actual bounded HP gain", c.HP, c.MP)
	}
	// The same packet/tick remains a replay even after the interval passes.
	c.HP = 40
	if err := s.regenerateAt(context.Background(), live, sink, q, landingAt.Add(30*time.Second)); err != nil || c.HP != 40 {
		t.Fatal("replayed recovery gained HP", err, c.HP)
	}
}

func TestRecoveryIntrapacketPairStillRequiresFinalGroundedLegalPose(t *testing.T) {
	for _, tc := range []struct {
		name           string
		firstX, firstY int16
		x, y, vx, vy   int16
		state          byte
		barrier        []byte
		settled        bool
	}{
		{name: "differentXY", firstX: 1, state: 4, settled: true},
		{name: "airborne", firstY: -10, y: -10, state: 4},
		{name: "horizontalVelocity", vx: 1, state: 4},
		{name: "verticalVelocity", vy: 1, state: 4},
		{name: "walkingAction", state: 0},
		{name: "relativeBetween", state: 4, settled: true, barrier: []byte{1, 0, 0, 0, 0, 4, 30, 0}},
		{name: "teleportBetween", state: 4, settled: true, barrier: []byte{3, 0, 0, 0, 0, 1, 0, 4, 30, 0}},
		{name: "opaqueBetween", state: 4, settled: true, barrier: []byte{17, 4, 30, 0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sink := &gameplayTestSink{}
			s, live, start := recoveryWorldFixture(t, sink)
			// An old same-position sample must not rescue a different final pair
			// or a packet whose continuity was interrupted.
			first := recoveryAbsoluteBody(tc.firstX, tc.firstY, 0, 0, 4)
			last := recoveryAbsoluteBody(tc.x, tc.y, tc.vx, tc.vy, tc.state)
			body := append([]byte(nil), first[:17]...)
			count := byte(2)
			if len(tc.barrier) > 0 {
				count++
			}
			body = append(body, count)
			body = append(body, first[18:36]...)
			body = append(body, tc.barrier...)
			body = append(body, last[18:]...)
			if err := s.moveScene(live, sink, protocol.NewReader(body)); err != nil {
				t.Fatal(err)
			}
			m := s.skills.members[1]
			if m.recoveryPose.allowsHP(s.sceneFor(live, sink), live.session.Character) != tc.settled {
				t.Fatal("wrong final zero-velocity candidate", m.recoveryPose)
			}
			requestAt := start.Add(time.Minute)
			if tc.settled {
				requestAt = m.regeneration[0][0].at.Add(10*time.Second - time.Millisecond)
			}
			if err := s.regenerateAt(context.Background(), live, sink, recoveryCommand(protocol.RecvRegeneration, 100, 10, 0), requestAt); err != nil || live.session.Character.HP != 20 || m.regeneration[0][0].seen {
				t.Fatal("unsafe pair healed or consumed HP budget", err)
			}
		})
	}
}

func TestRecoveryIntrapacketPairCannotPublishFromInvalidOrOldVisit(t *testing.T) {
	for _, stale := range []bool{false, true} {
		t.Run(map[bool]string{false: "truncated", true: "oldVisit"}[stale], func(t *testing.T) {
			sink := &gameplayTestSink{}
			s, live, start := recoveryWorldFixture(t, sink)
			m := s.skills.members[1]
			m.recoveryPose = recoveryPose{}
			one := recoveryAbsoluteBody(0, 0, 0, 0, 4)
			body := append([]byte(nil), one[:17]...)
			body = append(body, 2)
			body = append(body, one[18:36]...)
			body = append(body, one[18:]...)
			visit := s.sceneFor(live, sink)
			if stale {
				// Same owner, socket and map, but a new immutable visit.
				s.installScene(makeSceneVisit(live.session, live, sink), live.session)
			} else {
				body = body[:len(body)-1]
			}
			err := s.moveSceneVisit(visit, protocol.NewReader(body))
			if !stale && err == nil {
				t.Fatal("truncated movement accepted")
			}
			if m.recoveryPose.known {
				t.Fatal("invalid or stale packet established pose", m.recoveryPose)
			}
			if err := s.regenerateAt(context.Background(), live, sink, recoveryCommand(protocol.RecvRegeneration, 100, 10, 0), start.Add(time.Minute)); err != nil || live.session.Character.HP != 20 || m.regeneration[0][0].seen {
				t.Fatal("invalid or old visit pair gained HP", err)
			}
		})
	}
}

func TestRecoveryInterruptedFinalPairRestartsOnlyHPClocks(t *testing.T) {
	for name, barrier := range map[string][]byte{
		"relative":             {1, 0, 0, 0, 0, 4, 30, 0},
		"teleport":             {3, 0, 0, 0, 0, 1, 0, 4, 30, 0},
		"opaque":               {17, 4, 30, 0},
		"QtoPtoP":              recoveryAbsoluteBody(10, 0, 0, 0, 4)[18:36],
		"velocityThenZeroPair": recoveryAbsoluteBody(0, 0, 1, 0, 4)[18:36],
		"uninterrupted":        nil,
	} {
		t.Run(name, func(t *testing.T) {
			sink := &gameplayTestSink{}
			s, live, _ := recoveryWorldFixture(t, sink)
			m := s.skills.members[1]
			old := time.Now().Add(-time.Minute)
			for source := 0; source < 2; source++ {
				for component := 0; component < 2; component++ {
					m.regeneration[source][component] = regenerationCounter{at: old, tick: uint32(90 + source), seen: true}
				}
			}
			prior := m.regeneration
			one := recoveryAbsoluteBody(0, 0, 0, 0, 4)
			body := append([]byte(nil), one[:17]...)
			count := byte(2)
			if barrier != nil {
				count++
			}
			body = append(body, count)
			body = append(body, barrier...)
			body = append(body, one[18:36]...)
			body = append(body, one[18:]...)
			before := time.Now()
			if err := s.moveScene(live, sink, protocol.NewReader(body)); err != nil {
				t.Fatal(err)
			}
			after := time.Now()
			if !m.recoveryPose.allowsHP(s.sceneFor(live, sink), live.session.Character) {
				t.Fatal("final adjacent pair was lost")
			}
			if barrier == nil {
				if m.regeneration != prior {
					t.Fatal("uninterrupted stationary pair reset cadence")
				}
				return
			}
			reset := m.regeneration[0][0].at
			if reset.Before(before) || reset.After(after) {
				t.Fatal("HP clock borrowed old elapsed time", reset, before, after)
			}
			for source := 0; source < 2; source++ {
				hp, mp := m.regeneration[source][0], m.regeneration[source][1]
				if hp.at != reset || hp.tick != prior[source][0].tick || hp.seen != prior[source][0].seen || mp != prior[source][1] {
					t.Fatal("reset changed MP or native watermark", m.regeneration)
				}
			}
			for _, source := range []struct {
				op       uint16
				interval time.Duration
			}{{protocol.RecvPotentialRegeneration, 4 * time.Second}, {protocol.RecvRegeneration, 10 * time.Second}} {
				q := recoveryCommand(source.op, 100, 10, 0)
				if err := s.regenerateAt(context.Background(), live, sink, q, reset.Add(source.interval-time.Millisecond)); err != nil {
					t.Fatal(err)
				}
				if live.session.Character.HP != 20 {
					t.Fatal("old stable pose supplied premature HP", live.session.Character.HP)
				}
			}
			q := recoveryCommand(protocol.RecvRegeneration, 100, 10, 0)
			if err := s.regenerateAt(context.Background(), live, sink, q, reset.Add(10*time.Second)); err != nil || live.session.Character.HP != 30 {
				t.Fatal("new complete interval did not recover", err, live.session.Character.HP)
			}
		})
	}
}

func recoveryAbsoluteBody(x, y, vx, vy int16, state byte) []byte {
	w := protocol.NewRawWriter()
	w.U8(0)
	w.U32(0)
	w.U32(0)
	w.I16(x)
	w.I16(y)
	w.U32(0)
	w.U8(1)
	w.U8(0)
	w.I16(x)
	w.I16(y)
	w.I16(vx)
	w.I16(vy)
	w.I16(0)
	w.I16(0)
	w.I16(0)
	w.U8(state)
	w.I16(30)
	w.U8(0)
	w.Zero(8)
	return w.Packet()
}
func TestRecoveryPoseOnlyAfterAdmittedMoveAndStableGround(t *testing.T) {
	sink := &gameplayTestSink{}
	s, live, start := recoveryWorldFixture(t, sink)
	m := s.skills.members[1]
	m.recoveryPose = recoveryPose{}
	q := recoveryCommand(protocol.RecvPotentialRegeneration, 1, 9, 9)
	if err := s.regenerateAt(context.Background(), live, sink, q, start.Add(5*time.Second)); err != nil {
		t.Fatal(err)
	}
	if live.session.Character.HP != 20 || live.session.Character.MP != 29 {
		t.Fatal("post-entry unknown pose healedHP or blockedMP")
	}
	// Only a successfully parsed/applied move reaches the pose hook.
	if err := s.moveScene(live, sink, protocol.NewReader(recoveryAbsoluteBody(20, 0, 10, 0, 0))); err != nil {
		t.Fatal(err)
	}
	if m.recoveryPose.allowsHP(s.sceneFor(live, sink), live.session.Character) {
		t.Fatal("walking healedHP")
	}
	if err := s.moveScene(live, sink, protocol.NewReader(recoveryAbsoluteBody(20, 0, 0, 0, 4))); err != nil {
		t.Fatal(err)
	}
	if !m.recoveryPose.allowsHP(s.sceneFor(live, sink), live.session.Character) {
		t.Fatal("same-position stop on realWZ floor not admitted")
	}
	reset := m.regeneration[1][0].at
	if err := s.regenerateAt(context.Background(), live, sink, q, reset.Add(4*time.Second-time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if live.session.Character.HP != 20 {
		t.Fatal("standing reset interval bypassed")
	}
	if err := s.regenerateAt(context.Background(), live, sink, q, reset.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	if live.session.Character.HP != 24 {
		t.Fatal("grounded stable recovery lost")
	}
	if err := s.moveScene(live, sink, protocol.NewReader(recoveryAbsoluteBody(20, -10, 0, 0, 4))); err != nil {
		t.Fatal(err)
	}
	if m.recoveryPose.grounded || m.recoveryPose.allowsHP(s.sceneFor(live, sink), live.session.Character) {
		t.Fatal("airborne packet claimed ground")
	}
	old := m.recoveryPose
	bad := recoveryAbsoluteBody(20, 0, 0, 0, 4)
	bad = bad[:10]
	if err := s.moveScene(live, sink, protocol.NewReader(bad)); err == nil {
		t.Fatal("bad move accepted")
	}
	if m.recoveryPose != old {
		t.Fatal("invalid movement changed recovery proof")
	}
}
func TestRecoveryPoseRejectsWrongFootholdAndOldVisit(t *testing.T) {
	sink := &gameplayTestSink{}
	s, live, start := recoveryWorldFixture(t, sink)
	v := s.sceneFor(live, sink)
	ground, err := content.RecoveryGroundSupport(os.Getenv("MAPLEGO_WZ_ROOT"), 40000)
	if err != nil {
		t.Fatal(err)
	}
	move := protocol.PlayerMove{Pose: protocol.PlayerMovementPose{Known: true, State: 4, HasFoothold: true, Foothold: 2}}
	s.observeRecoveryMovement(v, live.session, move, ground, start)
	if s.skills.members[1].recoveryPose.grounded {
		t.Fatal("explicit wrong command14 FH accepted")
	}
	// Same room and socket do not revive the old proof on re-entry.
	session := live.session
	next := makeSceneVisit(session, live, sink)
	s.installScene(next, session)
	p := recoveryPose{visit: v, known: true, stable: true, grounded: true, state: 4}
	if p.allowsHP(next, domain.Character{}) {
		t.Fatal("old map-entry proof reused")
	}
}

func TestRecoveryExactNativeMarkerAndSettledStopOnQAFloor(t *testing.T) {
	for _, tc := range []struct {
		name, raw, sha string
		x              int
		previousX      int
	}{
		{"marker76", "47000203f34aadb09d971b15ffd70000000000030015ffd700000000000500000000000496000b040015ffd700000000000500000000000468011100000000000000000015ffd70015ffd700", "d08ba5ab56fff5220b2daa485c82ba73625951138d288774364427485e31bd4b", -235, -235},
		{"stop74", "47000203f34aadc2869c1b4fffd70051000000020054ffd70009000000030000000000045a000053ffd7000000000003000000000004a401110000000000000000004fffd70054ffd700", "91268d136057ae2677b1c07c9f04f957979e741b9afe479658f047e91afbe6b2", -173, -172},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, _ := hex.DecodeString(tc.raw)
			if fmt.Sprintf("%x", sha256.Sum256(raw)) != tc.sha {
				t.Fatal("native wire changed")
			}
			mapBytes, e := os.ReadFile(filepath.Join("..", "..", "..", "MapleStory_Legacy_Server", "wz", "Map.wz", "Map", "Map1", "180000001.img.xml"))
			if e != nil {
				t.Skip("original WZ unavailable", e)
			}
			sink := &gameplayTestSink{}
			_, live, _ := recoveryWorldFixture(t, sink)
			dir := filepath.Join(os.Getenv("MAPLEGO_WZ_ROOT"), "Map.wz", "Map", "Map1")
			if e = os.MkdirAll(dir, 0755); e != nil {
				t.Fatal(e)
			}
			if e = os.WriteFile(filepath.Join(dir, "180000001.img.xml"), mapBytes, 0644); e != nil {
				t.Fatal(e)
			}
			c := &live.session.Character
			c.MapID, c.X, c.Y, c.Job, c.Level = 180000001, tc.x, 215, 0, 10
			c.HP, c.MP, c.MaxHP, c.MaxMP, c.BaseMaxHP, c.BaseMaxMP = 408, 5, 416, 5, 416, 5
			c.Equipment = nil
			srv := &Server{skills: newSkillSpace(), lease: live.session.Lease}
			seedBoundScene(t, srv, live, sink)
			if e = srv.enterSkills(context.Background(), 1, live, sink); e != nil {
				t.Fatal(e)
			}
			if e = srv.moveScene(live, sink, protocol.NewReader(raw[2:])); e != nil {
				t.Fatal(e)
			}
			m := srv.skills.members[1]
			v := srv.sceneFor(live, sink)
			if !m.recoveryPose.allowsHP(v, *c) || m.recoveryPose.previousX != tc.previousX {
				t.Fatal("final real zero-V stop lost", m.recoveryPose)
			}
			at := m.regeneration[0][0].at
			if at.IsZero() || at != m.regeneration[1][0].at {
				t.Fatal("stop did not start both HP clocks")
			}
			q := recoveryCommand(protocol.RecvRegeneration, 100, 10, 0)
			if e = srv.regenerateAt(context.Background(), live, sink, q, at.Add(10*time.Second-time.Millisecond)); e != nil || c.HP != 408 || m.regeneration[0][0].seen {
				t.Fatal("premature recovery", e, c.HP)
			}
			// No additional movement, duplicate position packet or native frame clock is
			// invented. Server cadence alone matures the known final grounded stop.
			if e = srv.regenerateAt(context.Background(), live, sink, q, at.Add(10*time.Second)); e != nil || c.HP != 416 || !m.regeneration[0][0].seen {
				t.Fatal("native stop stayed permanently blocked", e, c.HP)
			}
		})
	}
}

func TestRecoverySettledCandidateResetsHPWithoutChangingMPOrWatermarks(t *testing.T) {
	for _, tc := range []struct {
		name   string
		prefix []byte
	}{
		{"previousDifferent", recoveryAbsoluteBody(1, 0, 0, 0, 4)[18:36]},
		{"previousVelocity", recoveryAbsoluteBody(0, 0, 9, 0, 4)[18:36]},
		{"relative", []byte{1, 0, 0, 0, 0, 4, 30, 0}},
		{"teleport", []byte{3, 0, 0, 0, 0, 1, 0, 4, 30, 0}},
		{"opaque", []byte{17, 4, 30, 0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sink := &gameplayTestSink{}
			srv, live, _ := recoveryWorldFixture(t, sink)
			m := srv.skills.members[1]
			old := time.Now().Add(-time.Hour)
			for source := 0; source < 2; source++ {
				for component := 0; component < 2; component++ {
					m.regeneration[source][component] = regenerationCounter{at: old, tick: 90, seen: true}
				}
			}
			prior := m.regeneration
			one := recoveryAbsoluteBody(0, 0, 0, 0, 4)
			body := append([]byte(nil), one[:17]...)
			body = append(body, 2)
			body = append(body, tc.prefix...)
			body = append(body, one[18:]...)
			before := time.Now()
			if e := srv.moveScene(live, sink, protocol.NewReader(body)); e != nil {
				t.Fatal(e)
			}
			at := m.regeneration[0][0].at
			if at.Before(before) || !m.recoveryPose.allowsHP(srv.sceneFor(live, sink), live.session.Character) {
				t.Fatal("candidate/reset missing", m.recoveryPose)
			}
			for source := 0; source < 2; source++ {
				if m.regeneration[source][0].at != at || m.regeneration[source][0].tick != 90 || !m.regeneration[source][0].seen || m.regeneration[source][1] != prior[source][1] {
					t.Fatal("MP/tick changed")
				}
			}
			for _, q := range []struct {
				op     uint16
				period time.Duration
				gain   int
			}{{protocol.RecvPotentialRegeneration, 4 * time.Second, 4}, {protocol.RecvRegeneration, 10 * time.Second, 10}} {
				oldHP := live.session.Character.HP
				cmd := recoveryCommand(q.op, 91, 10, 0)
				if e := srv.regenerateAt(context.Background(), live, sink, cmd, at.Add(q.period-time.Millisecond)); e != nil || live.session.Character.HP != oldHP {
					t.Fatal("borrowed old HP elapsed", e)
				}
				if e := srv.regenerateAt(context.Background(), live, sink, cmd, at.Add(q.period)); e != nil || live.session.Character.HP != oldHP+q.gain {
					t.Fatal("mature stop did not recover", e, live.session.Character.HP)
				}
			}
		})
	}
}

func TestRecoveryMarkerOnlyRetainsOnlyExactExistingPoseAndCadence(t *testing.T) {
	for _, name := range []string{"known", "knownPair", "unknown", "newVisit", "changedXY"} {
		t.Run(name, func(t *testing.T) {
			sink := &gameplayTestSink{}
			srv, live, _ := recoveryWorldFixture(t, sink)
			m := srv.skills.members[1]
			old := time.Now().Add(-time.Minute)
			for source := 0; source < 2; source++ {
				m.regeneration[source][0] = regenerationCounter{at: old, tick: 90, seen: true}
			}
			prior := m.regeneration
			oldPose := m.recoveryPose
			switch name {
			case "unknown":
				m.recoveryPose = recoveryPose{}
			case "newVisit":
				srv.installScene(makeSceneVisit(live.session, live, sink), live.session)
			case "changedXY":
				live.session.Character.X = 1
			}
			one := recoveryAbsoluteBody(100, 100, 0, 0, 4)
			b := append([]byte(nil), one[:17]...)
			b = append(b, 1, 11, 4)
			b = append(b, one[36:]...)
			if name == "knownPair" {
				actual := recoveryAbsoluteBody(0, 0, 0, 0, 4)
				b = append([]byte(nil), actual[:17]...)
				b = append(b, 3)
				b = append(b, actual[18:36]...)
				b = append(b, 11, 4)
				b = append(b, actual[18:]...)
			}
			if e := srv.moveScene(live, sink, protocol.NewReader(b)); e != nil {
				t.Fatal(e)
			}
			if name == "known" || name == "knownPair" {
				if m.recoveryPose != oldPose || m.regeneration != prior {
					t.Fatal("nonphysical marker reset proof/elapsed")
				}
			} else if m.recoveryPose.known || m.recoveryPose.allowsHP(srv.sceneFor(live, sink), live.session.Character) {
				t.Fatal("marker/header minted pose", m.recoveryPose)
			}
		})
	}
}

func TestRecoveryMovingFinalPoseAndPendingNeverRecoverHP(t *testing.T) {
	sink := &gameplayTestSink{}
	srv, live, _ := recoveryWorldFixture(t, sink)
	m := srv.skills.members[1]
	if e := srv.moveScene(live, sink, protocol.NewReader(recoveryAbsoluteBody(0, 0, 1, 0, 4))); e != nil {
		t.Fatal(e)
	}
	at := m.regeneration[0][0].at
	if e := srv.regenerateAt(context.Background(), live, sink, recoveryCommand(protocol.RecvRegeneration, 100, 10, 0), at.Add(time.Hour)); e != nil || live.session.Character.HP != 20 {
		t.Fatal("nonzero-V became settled", e)
	}
	if e := srv.moveScene(live, sink, protocol.NewReader(recoveryAbsoluteBody(0, 0, 0, 0, 4))); e != nil {
		t.Fatal(e)
	}
	at = m.regeneration[0][0].at
	srv.skills.partyPending = map[string]*partySkillTicket{"unknown": {peers: []partySkillPeer{{member: m, session: live.session}}}}
	prior := m.regeneration
	q := recoveryCommand(protocol.RecvRegeneration, 100, 10, 0)
	if e := srv.regenerateAt(context.Background(), live, sink, q, at.Add(10*time.Second)); e != nil || live.session.Character.HP != 20 || m.regeneration != prior {
		t.Fatal("pending healed/burned cadence", e)
	}
	delete(srv.skills.partyPending, "unknown")
	if e := srv.regenerateAt(context.Background(), live, sink, q, at.Add(10*time.Second)); e != nil || live.session.Character.HP != 30 {
		t.Fatal("resolved pending lost same request", e)
	}
}
