package world

import (
	"context"
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func periodicRecoveryFixture(t *testing.T, id, job int) (*Server, *liveCharacter, *gameplayTestSink, time.Time) {
	t.Helper()
	sink := &gameplayTestSink{}
	s, live, now := recoveryWorldFixture(t, sink)
	root := os.Getenv("MAPLEGO_WZ_ROOT")
	if err := os.MkdirAll(filepath.Join(root, "Skill.wz"), 0755); err != nil {
		t.Fatal(err)
	}
	file := "111.img.xml"
	sid := "1110000"
	if id == 11110000 {
		file = "1111.img.xml"
		sid = "11110000"
	}
	xml := `<imgdir name="skill"><imgdir name="skill"><imgdir name="` + sid + `"><imgdir name="common"><int name="maxLevel" value="20"/><string name="hp" value="5*x"/><string name="mp" value="3*x"/></imgdir></imgdir></imgdir></imgdir>`
	if err := os.WriteFile(filepath.Join(root, "Skill.wz", file), []byte(xml), 0644); err != nil {
		t.Fatal(err)
	}
	c := &live.session.Character
	c.Job, c.Level, c.INT = job, 120, 80
	c.Equipment[0].Potential = domain.EquipmentPotential{}
	c.Skills = []domain.SkillState{{ID: id, Level: 2}}
	return s, live, sink, now
}
func TestReviewedPassiveRecoveryFourSecondRuntimeAndNoDuplicateBase(t *testing.T) {
	for _, tc := range []struct{ id, job, hp int }{{1110000, 111, 20}, {11110000, 1111, 0}} {
		s, live, sink, start := periodicRecoveryFixture(t, tc.id, tc.job)
		m := s.skills.members[1]
		s.tickReviewedRecoveryAt(m, start)
		s.tickReviewedRecoveryAt(m, start.Add(4*time.Second-time.Millisecond))
		if live.session.Character.HP != 20 || live.session.Character.MP != 20 {
			t.Fatal("passive tick early")
		}
		s.tickReviewedRecoveryAt(m, start.Add(4*time.Second))
		if c := live.session.Character; c.HP != 20+tc.hp || c.MP != 37 || !live.dirty {
			t.Fatal("passive source benefit absent", tc, c.HP, c.MP)
		}
		s.tickReviewedRecoveryAt(m, start.Add(4*time.Second))
		if live.session.Character.MP != 37 {
			t.Fatal("same tick repeated")
		}
		q := recoveryCommand(protocol.RecvRegeneration, 99, 99, 99)
		// Make the normal source eligible; its base must still be suppressed for
		// the component already owned by the passive source.
		s.skills.members[1].regeneration = [2][2]regenerationCounter{}
		if err := s.regenerateAt(context.Background(), live, sink, q, start.Add(12*time.Second)); err != nil {
			t.Fatal(err)
		}
		wantHP := 20 + tc.hp
		if tc.id == 11110000 {
			wantHP += 10
		}
		if c := live.session.Character; c.HP != wantHP || c.MP != 37 {
			t.Fatal("ordinary91 duplicated passive base", c.HP, c.MP)
		}
		s.tickReviewedRecoveryAt(m, start.Add(time.Minute))
		if live.session.Character.MP != 54 {
			t.Fatal("passive catch-up replayed missed periods")
		}
	}
}

func TestBloodCirculationUpdatesLiveVitalsAfterFiveSeconds(t *testing.T) {
	s, live, start := recoveryWorldFixture(t, &gameplayTestSink{})
	c := &live.session.Character
	c.Job, c.Level = 570, 120
	c.VitalsModel = 0
	c.HP, c.MP, c.MaxHP, c.MaxMP = 1025, 63, 7193, 84
	c.Equipment[0].Potential = domain.EquipmentPotential{}
	c.Skills = []domain.SkillState{{ID: 5700005, Level: 15}}
	member := s.skills.members[1]

	s.tickReviewedRecoveryAt(member, start)
	s.tickReviewedRecoveryAt(member, start.Add(5*time.Second-time.Millisecond))
	if c.HP != 1025 || c.MP != 63 {
		t.Fatalf("recovered before interval: %d/%d", c.HP, c.MP)
	}
	s.tickReviewedRecoveryAt(member, start.Add(5*time.Second))
	s.tickReviewedRecoveryAt(member, start.Add(5*time.Second))
	if c.HP != 1600 || c.MP != 69 || !live.dirty {
		t.Fatalf("expected one 575 HP / 6 MP recovery, got %d/%d", c.HP, c.MP)
	}
}

func TestReviewedPassiveRecoveryPendingExpiredDeadAndFreshLevel(t *testing.T) {
	s, live, _, start := periodicRecoveryFixture(t, 1110000, 111)
	m := s.skills.members[1]
	m.passiveTicks = map[int]time.Time{1110000: start}
	s.skills.partyPending = map[string]*partySkillTicket{"unknown": {peers: []partySkillPeer{{member: m, session: live.session}}}}
	s.tickReviewedRecoveryAt(m, start.Add(4*time.Second))
	if m.passiveTicks[1110000] != start || live.dirty {
		t.Fatal("pending burned passive budget")
	}
	delete(s.skills.partyPending, "unknown")
	live.session.Character.Skills[0].Level = 3
	s.tickReviewedRecoveryAt(m, start.Add(4*time.Second))
	if live.session.Character.HP != 45 || live.session.Character.MP != 40 {
		t.Fatal("fresh level not recomputed")
	}
	live.session.Character.Skills[0].ExpiresAt = start.Add(5 * time.Second)
	last := m.passiveTicks[1110000]
	s.tickReviewedRecoveryAt(m, start.Add(8*time.Second))
	if m.passiveTicks[1110000] != last || live.session.Character.HP != 45 {
		t.Fatal("expired passive healed/burned clock")
	}
	live.session.Character.Skills[0].ExpiresAt = time.Time{}
	live.session.Character.HP = 0
	s.tickReviewedRecoveryAt(m, start.Add(12*time.Second))
	if live.session.Character.HP != 0 || m.passiveTicks[1110000] != last {
		t.Fatal("passive resurrected")
	}
}
func TestReviewedPassiveRecoveryWaitCannotUseNewVisit(t *testing.T) {
	s, live, sink, start := periodicRecoveryFixture(t, 1110000, 111)
	m := s.skills.members[1]
	m.passiveTicks = map[int]time.Time{1110000: start}
	release, err := s.lockScenePublication(1)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { s.tickReviewedRecoveryAt(m, start.Add(4*time.Second)); close(done) }()
	waitRecoveryPersistenceHeld(t, live)
	session := live.session
	session.Character.HP = 7
	s.installScene(makeSceneVisit(session, live, sink), session)
	release()
	<-done
	if live.session.Character.HP != 7 || m.passiveTicks[1110000] != start || live.dirty || len(sink.packets) != 0 {
		t.Fatal("old passive callback reached new visit")
	}
}
func TestReviewedPassiveRecoveryEffectiveINTAndActualMaxClamp(t *testing.T) {
	s, live, _, start := periodicRecoveryFixture(t, 1110000, 111)
	m := s.skills.members[1]
	root := os.Getenv("MAPLEGO_WZ_ROOT")
	p := filepath.Join(root, "Item.wz/ItemOption.img.xml")
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	extra := `<imgdir name="010001"><imgdir name="info"><int name="weight" value="1"/></imgdir><imgdir name="level"><imgdir name="1"><int name="incINT" value="40"/></imgdir></imgdir></imgdir>`
	raw = append(raw[:len(raw)-len("</imgdir>")], []byte(extra+"</imgdir>")...)
	if err = os.WriteFile(p, raw, 0644); err != nil {
		t.Fatal(err)
	}
	live.session.Character.Equipment[0].Potential = domain.EquipmentPotential{Present: true, RawState: 17, Options: [5]uint16{10001}}
	m.passiveTicks = map[int]time.Time{1110000: start}
	s.tickReviewedRecoveryAt(m, start.Add(4*time.Second))
	if c := live.session.Character; c.HP != 40 || c.MP != 41 || c.INT != 80 || c.BaseMaxHP != 100 {
		t.Fatal("potential INT missing or mutated base", c.HP, c.MP, c.INT)
	}
	live.session.Character.HP, live.session.Character.MP = 99, 99
	s.tickReviewedRecoveryAt(m, start.Add(8*time.Second))
	if c := live.session.Character; c.HP != 100 || c.MP != 100 {
		t.Fatal("passive overfilled maxima")
	}
}
func TestReviewedPassiveAndNativePotentialRecoveryHaveIndependentSources(t *testing.T) {
	s, live, sink, start := periodicRecoveryFixture(t, 1110000, 111)
	m := s.skills.members[1]
	live.session.Character.Equipment[0].Potential = domain.EquipmentPotential{Present: true, RawState: 17, Options: [5]uint16{10151, 10151, 10156, 10156, 10156}}
	m.passiveTicks = map[int]time.Time{1110000: start}
	s.tickReviewedRecoveryAt(m, start.Add(4*time.Second))
	if c := live.session.Character; c.HP != 40 || c.MP != 37 {
		t.Fatal("potentialflat leaked into passive source", c.HP, c.MP)
	}
	if err := s.regenerateAt(context.Background(), live, sink, recoveryCommand(protocol.RecvPotentialRegeneration, 1, 99, 99), start.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	if c := live.session.Character; c.HP != 44 || c.MP != 46 {
		t.Fatal("native92 absent or doubled with passive", c.HP, c.MP)
	}
	if err := s.regenerateAt(context.Background(), live, sink, recoveryCommand(protocol.RecvRegeneration, 1, 99, 99), start.Add(10*time.Second)); err != nil {
		t.Fatal(err)
	}
	if c := live.session.Character; c.HP != 44 || c.MP != 46 {
		t.Fatal("ordinary base duplicated passive base", c.HP, c.MP)
	}
	// Expiry releases ordinary-source ownership immediately, without waiting for
	// or replaying the old passive timer, and without consuming its source clock.
	live.session.Character.Skills[0].ExpiresAt = start.Add(11 * time.Second)
	if err := s.regenerateAt(context.Background(), live, sink, recoveryCommand(protocol.RecvRegeneration, 2, 99, 99), start.Add(12*time.Second)); err != nil {
		t.Fatal(err)
	}
	if c := live.session.Character; c.HP != 54 || c.MP != 49 {
		t.Fatal("expired passive kept normal source ownership", c.HP, c.MP)
	}
	last := m.passiveTicks[1110000]
	s.tickReviewedRecoveryAt(m, start.Add(12*time.Second))
	if m.passiveTicks[1110000] != last || live.session.Character.HP != 54 {
		t.Fatal("expired passive repeated after native recovery")
	}
}

func TestReviewedPassiveRecoveryScaledWorkerAndFlatPotential(t *testing.T) {
	for _, tc := range []struct {
		rate   string
		hp, mp int
	}{{"2", 40, 34}, {"3", 60, 51}, {"0", 20, 17}} {
		s, live, sink, start := periodicRecoveryFixture(t, 1110000, 111)
		root := os.Getenv("MAPLEGO_WZ_ROOT")
		// A distinct map avoids rewriting already cached facts from fixture entry.
		path := filepath.Join(root, "Map.wz/Map/Map0/000040001.img.xml")
		data, err := os.ReadFile(filepath.Join(root, "Map.wz/Map/Map0/000040000.img.xml"))
		if err != nil {
			t.Fatal(err)
		}
		data = []byte(strings.Replace(string(data), "<imgdir name=\"info\"/>", "<imgdir name=\"info\"><float name=\"recovery\" value=\""+tc.rate+"\"/></imgdir>", 1))
		if err = os.WriteFile(path, data, 0644); err != nil {
			t.Fatal(err)
		}
		live.session.Character.MapID = 40001
		m := s.skills.members[1]
		m.mapID = 40001
		seedBoundScene(t, s, live, sink)
		ground, err := content.RecoveryGroundSupport(root, 40001)
		if err != nil {
			t.Fatal(err)
		}
		move := protocol.PlayerMove{Pose: protocol.PlayerMovementPose{Known: true, State: 4}}
		visit := s.sceneFor(live, sink)
		s.observeRecoveryMovement(visit, live.session, move, ground, start)
		s.observeRecoveryMovement(visit, live.session, move, ground, start)
		live.session.Character.Equipment[0].Potential = domain.EquipmentPotential{Present: true, RawState: 17, Options: [5]uint16{10151, 10151, 10156, 10156, 10156}}
		m.passiveTicks = map[int]time.Time{1110000: start}
		s.tickReviewedRecoveryAt(m, start.Add(4*time.Second))
		if c := live.session.Character; c.HP != 20+tc.hp || c.MP != 20+tc.mp || !live.dirty {
			t.Fatal("scaled worker did not apply dirty vitals", tc, c.HP, c.MP)
		}
		if err = s.regenerateAt(context.Background(), live, sink, recoveryCommand(protocol.RecvPotentialRegeneration, 1, 99, 99), start.Add(4*time.Second)); err != nil {
			t.Fatal(err)
		}
		if c := live.session.Character; c.HP != 24+tc.hp || c.MP != 29+tc.mp {
			t.Fatal("potential92 was rate multiplied", tc, c.HP, c.MP)
		}
		if err = s.regenerateAt(context.Background(), live, sink, recoveryCommand(protocol.RecvRegeneration, 1, 99, 99), start.Add(10*time.Second)); err != nil {
			t.Fatal(err)
		}
		if c := live.session.Character; c.HP != 24+tc.hp || c.MP != 29+tc.mp {
			t.Fatal("ordinary base duplicated", tc, c.HP, c.MP)
		}
	}
}
