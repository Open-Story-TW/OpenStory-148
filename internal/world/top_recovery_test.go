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

func bindTopRecoveryMap(t *testing.T, s *Server, live *liveCharacter, sink packetSink, start time.Time) {
	t.Helper()
	root := os.Getenv("MAPLEGO_WZ_ROOT")
	data, err := os.ReadFile(filepath.Join(root, "Map.wz/Map/Map0/000040000.img.xml"))
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), `<imgdir name="info"/>`, `<imgdir name="info"><float name="recovery" value="3"/></imgdir>`, 1))
	if err = os.WriteFile(filepath.Join(root, "Map.wz/Map/Map0/000040002.img.xml"), data, 0644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "Character.wz/Coat")
	if err = os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "01040002.img.xml"), []byte(`<imgdir name="top"><imgdir name="info"><float name="recovery" value="1.5"/></imgdir></imgdir>`), 0644); err != nil {
		t.Fatal(err)
	}
	live.session.Character.MapID = 40002
	s.skills.members[1].mapID = 40002
	live.session.Character.Equipment = append(live.session.Character.Equipment, domain.Equipment{ID: 11, ItemID: 1040002, Slot: -5})
	seedBoundScene(t, s, live, sink)
	ground, err := content.RecoveryGroundSupport(root, 40002)
	if err != nil {
		t.Fatal(err)
	}
	move := protocol.PlayerMove{Pose: protocol.PlayerMovementPose{Known: true, State: 4}}
	visit := s.sceneFor(live, sink)
	s.observeRecoveryMovement(visit, live.session, move, ground, start)
	s.observeRecoveryMovement(visit, live.session, move, ground, start)
}
func TestTopRecoveryOrdinaryActualDirtyAndExpiryFlatPotential(t *testing.T) {
	sink := &gameplayTestSink{}
	s, live, start := recoveryWorldFixture(t, sink)
	bindTopRecoveryMap(t, s, live, sink, start)
	live.session.Character.Equipment[1].ExpiresAt = start.Add(15 * time.Second).UnixMilli()
	run := func(op uint16, tick uint32, at time.Duration) {
		t.Helper()
		if err := s.regenerateAt(context.Background(), live, sink, recoveryCommand(op, tick, 99, 99), start.Add(at)); err != nil {
			t.Fatal(err)
		}
	}
	run(protocol.RecvRegeneration, 1, 10*time.Second)
	if c := live.session.Character; c.HP != 65 || c.MP != 33 || !live.dirty || s.online[1].Character.HP != 65 {
		t.Fatal("ordinary top benefit not applied", c.HP, c.MP)
	}
	run(protocol.RecvPotentialRegeneration, 1, 14*time.Second)
	if c := live.session.Character; c.HP != 69 || c.MP != 42 {
		t.Fatal("top recovery multiplier leaked to92", c.HP, c.MP)
	}
	run(protocol.RecvRegeneration, 2, 20*time.Second)
	if c := live.session.Character; c.HP != 99 || c.MP != 51 {
		t.Fatal("expired top still applied", c.HP, c.MP)
	}
	run(protocol.RecvRegeneration, 2, 30*time.Second)
	if c := live.session.Character; c.HP != 99 || c.MP != 51 {
		t.Fatal("ordinary wire replay healed", c.HP, c.MP)
	}
}
func TestTopRecoveryDoesNotMultiplyFourSecondSkillSource(t *testing.T) {
	s, live, sink, start := periodicRecoveryFixture(t, 1110000, 111)
	bindTopRecoveryMap(t, s, live, sink, start)
	live.session.Character.Equipment[0].Potential = domain.EquipmentPotential{Present: true, RawState: 17, Options: [5]uint16{10151, 10151, 10156, 10156, 10156}}
	m := s.skills.members[1]
	m.passiveTicks = map[int]time.Time{1110000: start}
	s.tickReviewedRecoveryAt(m, start.Add(4*time.Second))
	if c := live.session.Character; c.HP != 80 || c.MP != 71 || !live.dirty {
		t.Fatal("top multiplier leaked to4s", c.HP, c.MP)
	}
	if err := s.regenerateAt(context.Background(), live, sink, recoveryCommand(protocol.RecvPotentialRegeneration, 1, 99, 99), start.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	if c := live.session.Character; c.HP != 84 || c.MP != 80 {
		t.Fatal("top multiplier leaked to92", c.HP, c.MP)
	}
	if err := s.regenerateAt(context.Background(), live, sink, recoveryCommand(protocol.RecvRegeneration, 1, 99, 99), start.Add(10*time.Second)); err != nil {
		t.Fatal(err)
	}
	if c := live.session.Character; c.HP != 84 || c.MP != 80 {
		t.Fatal("top ordinary duplicated passive", c.HP, c.MP)
	}
}

func TestTopRecoveryInvalidFactsDoNotSpendRuntimeBudget(t *testing.T) {
	sink := &gameplayTestSink{}
	s, live, start := recoveryWorldFixture(t, sink)
	bindTopRecoveryMap(t, s, live, sink, start)
	path := filepath.Join(os.Getenv("MAPLEGO_WZ_ROOT"), "Character.wz/Coat/01040003.img.xml")
	if err := os.WriteFile(path, []byte(`<imgdir name="top"><imgdir name="info"><float name="recovery" value="NaN"/></imgdir></imgdir>`), 0644); err != nil {
		t.Fatal(err)
	}
	live.session.Character.Equipment[1].ItemID = 1040003
	q := recoveryCommand(protocol.RecvRegeneration, 1, 99, 99)
	if err := s.regenerateAt(context.Background(), live, sink, q, start.Add(10*time.Second)); err != nil {
		t.Fatal(err)
	}
	if c := live.session.Character; c.HP != 20 || c.MP != 20 || live.dirty || s.skills.members[1].regeneration[0][0].seen || s.skills.members[1].regeneration[0][1].seen {
		t.Fatal("invalid template changed vitals or spent clock", c.HP, c.MP)
	}
	live.session.Character.Equipment[1].ItemID = 1040002
	if err := s.regenerateAt(context.Background(), live, sink, q, start.Add(10*time.Second)); err != nil {
		t.Fatal(err)
	}
	if c := live.session.Character; c.HP != 65 || c.MP != 33 {
		t.Fatal("same tick not available after corrected source", c.HP, c.MP)
	}
}
