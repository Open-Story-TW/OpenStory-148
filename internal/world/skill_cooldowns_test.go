package world

import (
	"encoding/binary"
	"errors"
	"testing"
	"time"

	"maple-go-server/internal/domain"
)

func TestCooldownExpirySendsExplicitNativeRemovalWithoutActiveBuff(t *testing.T) {
	now := time.Now()
	s := &Server{maps: newMapSpace(800), skills: newSkillSpace()}
	live := buffScopePlayer(1, 0)
	live.session.Character.SkillCooldowns = []domain.SkillCooldown{{ID: 1002, ExpiresAt: now.Add(time.Second)}}
	sink := &gameplayTestSink{}
	enterBuffScope(t, s, live, sink)
	member := s.skills.members[1]
	before := sink.count(0x19f)
	s.expireSkillCooldowns(member, now)
	if sink.count(0x19f) != before {
		t.Fatal("cleared an unexpired cooldown")
	}
	// Model the authoritative SQL reader filtering the expired row before
	// this callback. The native-announced deadline must still be remembered.
	live.session.Character.SkillCooldowns = nil
	s.expireSkillCooldowns(member, now.Add(2*time.Second))
	packets := buffPackets(sink)
	p := packets[len(packets)-1]
	if len(p) != 10 || binary.LittleEndian.Uint16(p) != 0x19f || binary.LittleEndian.Uint32(p[2:]) != 1002 || binary.LittleEndian.Uint32(p[6:]) != 0 {
		t.Fatalf("missing native removal: %x", p)
	}
	s.expireSkillCooldowns(member, now.Add(3*time.Second))
	if sink.count(0x19f) != before+1 {
		t.Fatal("already-delivered expiry was repeated")
	}
}

type cooldownRetrySink struct {
	gameplayTestSink
	fail bool
}

func (s *cooldownRetrySink) WritePacket(packet []byte) error {
	if s.fail && len(packet) >= 2 && binary.LittleEndian.Uint16(packet) == 0x19f {
		return errors.New("test cooldown transport failure")
	}
	return s.gameplayTestSink.WritePacket(packet)
}

func TestCooldownExpiryRetriesFailedWriteAndRejectsOldConnection(t *testing.T) {
	now := time.Now()
	s := &Server{maps: newMapSpace(800), skills: newSkillSpace()}
	live := buffScopePlayer(1, 0)
	live.session.Character.SkillCooldowns = []domain.SkillCooldown{{ID: 1002, ExpiresAt: now.Add(-time.Second)}}
	sink := &cooldownRetrySink{}
	enterBuffScope(t, s, live, sink)
	old := s.skills.members[1]
	sink.fail = true
	s.expireSkillCooldowns(old, now)
	if _, pending := old.cooldowns[1002]; !pending {
		t.Fatal("failed write discarded pending expiry")
	}
	sink.fail = false
	s.expireSkillCooldowns(old, now)
	if sink.count(0x19f) != 1 || len(old.cooldowns) != 0 {
		t.Fatal("successful retry did not clear pending expiry")
	}
	replacement := buffScopePlayer(1, 0)
	replacement.session.SessionID = "replacement-cooldown-session"
	replacement.session.Generation++
	newSink := &gameplayTestSink{}
	s.maps.Leave(1, live.session.Character.MapID, 1, sink)
	enterBuffScope(t, s, replacement, newSink)
	old.cooldowns[1002] = now.Add(-time.Second)
	s.expireSkillCooldowns(old, now)
	if sink.count(0x19f) != 1 || newSink.count(0x19f) != 0 {
		t.Fatal("old connection delivered into replacement session")
	}
}

func TestCooldownExpiryCannotClearRecastOrReplacementVisit(t *testing.T) {
	now := time.Now()
	s := &Server{maps: newMapSpace(800), skills: newSkillSpace()}
	live := buffScopePlayer(1, 0)
	live.session.Character.SkillCooldowns = []domain.SkillCooldown{{ID: 1002, ExpiresAt: now.Add(-time.Second)}}
	sink := &gameplayTestSink{}
	enterBuffScope(t, s, live, sink)
	old := s.skills.members[1]
	live.session.Character.SkillCooldowns[0].ExpiresAt = now.Add(time.Minute)
	s.expireSkillCooldowns(old, now)
	if sink.count(0x19f) != 0 || !old.cooldowns[1002].Equal(now.Add(time.Minute)) {
		t.Fatal("old deadline cancelled a newer cooldown")
	}
	// The same live connection changes map; its native cooldown map survives.
	s.maps.Leave(1, live.session.Character.MapID, 1, sink)
	live.session.Character.MapID = 50000
	live.session.Character.SkillCooldowns = nil
	enterBuffScope(t, s, live, sink)
	current := s.skills.members[1]
	s.expireSkillCooldowns(old, now.Add(2*time.Minute))
	if sink.count(0x19f) != 0 {
		t.Fatal("old visit published cooldown expiry")
	}
	s.expireSkillCooldowns(current, now.Add(2*time.Minute))
	if sink.count(0x19f) != 1 {
		t.Fatal("map transition lost the native-announced deadline")
	}
}

func TestEntryClearsNativeCooldownFilteredDuringHandoff(t *testing.T) {
	s := &Server{maps: newMapSpace(800), skills: newSkillSpace()}
	live := buffScopePlayer(1, 0)
	live.session.Character.Skills = []domain.SkillState{{ID: 1002, Level: 3}}
	// There is no previous world member and no SQL cooldown row in the loaded
	// snapshot; the same native process may still retain its expired key.
	sink := &gameplayTestSink{}
	enterBuffScope(t, s, live, sink)
	found := false
	for _, p := range buffPackets(sink) {
		if len(p) == 10 && binary.LittleEndian.Uint16(p) == 0x19f && binary.LittleEndian.Uint32(p[2:]) == 1002 && binary.LittleEndian.Uint32(p[6:]) == 0 {
			found = true
		}
	}
	if !found || len(s.skills.members[1].cooldowns) != 0 {
		t.Fatal("handoff did not remove the stale native cooldown key")
	}
}
