package world

import (
	"context"
	"testing"
	"time"

	"maple-go-server/internal/combat"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/skills"
)

const testGateSkill = 9990731

func init() {
	skills.Register(skills.Handler{ID: testGateSkill, Kind: skills.KindFieldObject, Attack: &combat.AttackSpec{
		Effect: combat.Effect{SkillID: testGateSkill, Time: 30 * time.Second}, Field: &combat.FieldSpec{Kind: combat.FieldOpenGate}}})
}

func TestOpenGatePairUseReplaceAndExpiry(t *testing.T) {
	s, lives, sinks, _ := sharedWorldFixture(t)
	owner := lives[0]
	now := time.Now()
	cast := func(x, y int, at time.Time) {
		t.Helper()
		owner.mu.Lock()
		owner.session.Character.X, owner.session.Character.Y = x, y
		owner.mu.Unlock()
		if _, err := s.SpawnFieldObject(1, owner, testGateSkill, 1, at); err != nil {
			t.Fatal(err)
		}
	}
	cast(-100, 0, now)
	cast(300, 0, now.Add(time.Second))
	if sinks[1].count(protocol.SendOpenGateCreated) != 2 {
		t.Fatal("gates not broadcast to the map")
	}
	// Using: the owner at the first gate goes to the second; a stranger is refused.
	ownerAcks, strangerAcks := sinks[0].count(protocol.SendUpdateStats), sinks[1].count(protocol.SendUpdateStats)
	if err := s.useOpenGate(owner, sinks[0], 1, protocol.UseOpenGate{OwnerID: 1, X: -100, Y: 0}); err != nil {
		t.Fatal(err)
	}
	if c := owner.session.Character; c.X != 300 {
		t.Fatalf("a user away from the gate was moved: %d", c.X)
	}
	owner.mu.Lock()
	owner.session.Character.X = -95
	owner.mu.Unlock()
	if err := s.useOpenGate(owner, sinks[0], 1, protocol.UseOpenGate{OwnerID: 1, X: -100, Y: 0}); err != nil {
		t.Fatal(err)
	}
	if c := owner.session.Character; c.X != 300 || c.Y != 0 {
		t.Fatalf("owner not moved to the second gate: %d,%d", c.X, c.Y)
	}
	stranger := lives[1]
	stranger.mu.Lock()
	stranger.session.Character.X, stranger.session.Character.Y = 300, 0
	stranger.mu.Unlock()
	if err := s.useOpenGate(stranger, sinks[1], 1, protocol.UseOpenGate{OwnerID: 1, X: 300, Y: 0}); err != nil {
		t.Fatal(err)
	}
	if c := stranger.session.Character; c.X != 300 {
		t.Fatalf("a non-party user went through the gate: %d", c.X)
	}
	if sinks[0].count(protocol.SendUpdateStats)-ownerAcks != 2 || sinks[1].count(protocol.SendUpdateStats)-strangerAcks != 1 {
		t.Fatal("gate use must always clear the exclusive request")
	}
	// A third cast replaces the older (first) gate in its slot.
	cast(600, 0, now.Add(2*time.Second))
	s.gameplay.mu.Lock()
	first, second := s.gameplay.gates[gateKey{1, 1, false}], s.gameplay.gates[gateKey{1, 1, true}]
	s.gameplay.mu.Unlock()
	if first == nil || first.x != 600 || second == nil || second.x != 300 || sinks[1].count(protocol.SendOpenGateRemoved) != 1 {
		t.Fatalf("replace oldest: %+v %+v", first, second)
	}
	// A late joiner draws both gates, the first before the second.
	late := &gameplayTestSink{}
	s.leaveGameplay(1, 40000, 2, sinks[1])
	if err := s.enterGameplay(1, lives[1], late); err != nil {
		t.Fatal(err)
	}
	if late.count(protocol.SendOpenGateCreated) != 2 {
		t.Fatal("late joiner missing gates")
	}
	s.gameplay.tickGates(now.Add(31 * time.Second))
	if late.count(protocol.SendOpenGateRemoved) != 1 {
		t.Fatal("the older gate did not expire")
	}
	s.gameplay.tickGates(now.Add(33 * time.Second))
	if late.count(protocol.SendOpenGateRemoved) != 2 {
		t.Fatal("the newer gate did not expire")
	}
}

func TestOpenGateLeavesWithItsOwner(t *testing.T) {
	s, lives, sinks, _ := sharedWorldFixture(t)
	if _, err := s.SpawnFieldObject(1, lives[0], testGateSkill, 1, time.Now()); err != nil {
		t.Fatal(err)
	}
	s.leaveGameplay(1, 40000, 1, sinks[0])
	s.gameplay.tickGates(time.Now())
	if sinks[1].count(protocol.SendOpenGateRemoved) != 1 || len(s.gameplay.gates) != 0 {
		t.Fatal("gate stayed after its owner left the map")
	}
}

// The 0xE8 request reaches the gate handler; a malformed one still clears
// the client's exclusive request.
func TestOpenGateRequestRoute(t *testing.T) {
	s, lives, sinks, _ := sharedWorldFixture(t)
	before := sinks[0].count(protocol.SendUpdateStats)
	handled, err := s.handleGameplay(context.Background(), 1, lives[0], sinks[0], protocol.RecvUseOpenGate, protocol.NewReader([]byte{1, 2}))
	if !handled || err != nil || sinks[0].count(protocol.SendUpdateStats) != before+1 {
		t.Fatal("0xE8 not routed to the open gate handler", handled, err)
	}
}
