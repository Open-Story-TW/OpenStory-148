package world

import (
	"sync"
	"testing"
	"time"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

type memSink struct {
	mu   sync.Mutex
	pkts [][]byte
}

func (m *memSink) WritePacket(p []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pkts = append(m.pkts, append([]byte(nil), p...))
	return nil
}

func (m *memSink) opcodes() []uint16 {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]uint16, 0, len(m.pkts))
	for _, p := range m.pkts {
		if len(p) >= 2 {
			out = append(out, uint16(p[0])|uint16(p[1])<<8)
		}
	}
	return out
}

func TestMapAOISpawnMoveRemove(t *testing.T) {
	space := newMapSpace(500)
	aSink, bSink := &memSink{}, &memSink{}
	a := domain.Character{ID: 1, Name: "A", Level: 1, HP: 50, MaxHP: 50, MP: 50, MaxMP: 50, X: 0, Y: 0, Revision: 1}
	b := domain.Character{ID: 2, Name: "B", Level: 1, HP: 50, MaxHP: 50, MP: 50, MaxMP: 50, X: 100, Y: 0, Revision: 1}
	space.Enter(1, 10000, a, aSink)
	space.Enter(1, 10000, b, bSink)
	if space.VisibleCount(1, 10000, 1) != 1 || space.VisibleCount(1, 10000, 2) != 1 {
		t.Fatalf("expected mutual visibility")
	}
	aOps := aSink.opcodes()
	bOps := bSink.opcodes()
	if len(aOps) == 0 || aOps[0] != protocol.SendSpawnPlayer || len(bOps) == 0 || bOps[0] != protocol.SendSpawnPlayer {
		t.Fatalf("spawn exchange a=%v b=%v", aOps, bOps)
	}
	move := protocol.EncodeAbsolutePlayerMove(0, 0, 120, 0)
	r := protocol.NewReader(move)
	r.U16()
	parsed, err := protocol.ParsePlayerMove(r)
	if err != nil {
		t.Fatal(err)
	}
	a.X, a.Y = parsed.X, parsed.Y
	bSink.pkts = nil
	space.Move(1, 10000, 1, a, parsed.StartX, parsed.StartY, parsed.Fragments)
	ops := bSink.opcodes()
	if len(ops) == 0 || ops[0] != protocol.SendMovePlayer {
		t.Fatalf("expected move broadcast, got %v", ops)
	}
	// Move A far away → mutual despawn.
	a.X = 5000
	aSink.pkts, bSink.pkts = nil, nil
	space.Move(1, 10000, 1, a, 120, 0, parsed.Fragments)
	if space.VisibleCount(1, 10000, 1) != 0 || space.VisibleCount(1, 10000, 2) != 0 {
		t.Fatalf("expected leave AOI")
	}
	if aSink.opcodes()[0] != protocol.SendRemovePlayer || bSink.opcodes()[0] != protocol.SendRemovePlayer {
		t.Fatalf("expected remove on AOI exit")
	}
	space.Leave(1, 10000, 2)
	space.Leave(1, 10000, 1)
}

func TestMapAOIIgnoresOtherChannel(t *testing.T) {
	space := newMapSpace(800)
	aSink, bSink := &memSink{}, &memSink{}
	a := domain.Character{ID: 1, Name: "A", Level: 1, HP: 1, MaxHP: 1, MP: 1, MaxMP: 1, X: 0, Y: 0, Revision: 1}
	b := domain.Character{ID: 2, Name: "B", Level: 1, HP: 1, MaxHP: 1, MP: 1, MaxMP: 1, X: 0, Y: 0, Revision: 1}
	space.Enter(1, 10000, a, aSink)
	space.Enter(2, 10000, b, bSink)
	if space.VisibleCount(1, 10000, 1) != 0 {
		t.Fatal("cross-channel visibility")
	}
}

func TestDelayedMovementCannotMutateReplacementMember(t *testing.T) {
	space := newMapSpace(500)
	oldSink, currentSink, peerSink := &memSink{}, &memSink{}, &memSink{}
	actor := domain.Character{ID: 1, Name: "A", Level: 1, HP: 50, MaxHP: 50, MP: 50, MaxMP: 50, X: 10, Y: 20, Revision: 1}
	peer := actor
	peer.ID, peer.Name, peer.X = 2, "B", 100
	space.Enter(1, 10000, actor, oldSink)
	space.Enter(1, 10000, peer, peerSink)
	space.Enter(1, 10000, actor, currentSink)
	member := space.rooms[mapKey{1, 10000}][1]
	oldSink.pkts, currentSink.pkts, peerSink.pkts = nil, nil, nil
	var spawnCallbacks int
	space.spawnState = func(mapKey, *mapMember, *mapMember) { spawnCallbacks++ }

	stale := actor
	stale.X, stale.Name = 9000, "stale"
	space.Move(1, 10000, 1, stale, 10, 20, []byte{1}, oldSink)
	if member.x != 10 || member.y != 20 || member.look.Name != "A" || space.VisibleCount(1, 10000, 1) != 1 || space.VisibleCount(1, 10000, 2) != 1 {
		t.Fatal("old movement mutated replacement position, appearance or AOI")
	}
	if len(oldSink.pkts)+len(currentSink.pkts)+len(peerSink.pkts) != 0 || spawnCallbacks != 0 {
		t.Fatal("old movement published packets or spawn state")
	}

	actor.X = 9000
	space.Move(1, 10000, 1, actor, 10, 20, []byte{1}, currentSink)
	if member.x != 9000 || space.VisibleCount(1, 10000, 1) != 0 || space.VisibleCount(1, 10000, 2) != 0 {
		t.Fatal("current socket could not update its member")
	}
	if len(currentSink.pkts) != 1 || len(peerSink.pkts) != 1 || currentSink.opcodes()[0] != protocol.SendRemovePlayer || peerSink.opcodes()[0] != protocol.SendRemovePlayer {
		t.Fatal("current socket did not publish the AOI exit")
	}
}

func TestEvanDragonVisibility(t *testing.T) {
	space := newMapSpace(500)
	aSink, bSink := &memSink{}, &memSink{}
	evan := domain.Character{ID: 1, Name: "E", Level: 20, Job: 2210, X: 0, Y: 0, Revision: 1}
	peer := domain.Character{ID: 2, Name: "P", Level: 20, X: 100, Y: 0, Revision: 1}
	space.Enter(1, 10000, evan, aSink)
	if ops := aSink.opcodes(); len(ops) != 1 || ops[0] != protocol.SendSpawnDragon {
		t.Fatalf("owner dragon on entry %x", ops)
	}
	space.Enter(1, 10000, peer, bSink)
	if ops := bSink.opcodes(); len(ops) != 2 || ops[0] != protocol.SendSpawnPlayer || ops[1] != protocol.SendSpawnDragon {
		t.Fatalf("late joiner did not get the dragon %x", ops)
	}
	frag := []byte{1, 0, 30, 0, 40, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 4, 0, 0}
	mv, err := protocol.ReadDragonMove(protocol.NewReader(append([]byte{0, 0, 0, 0, 0, 0, 0, 0}, frag...)))
	if err != nil {
		t.Fatal(err)
	}
	bSink.pkts = nil
	space.MoveDragon(1, 10000, 2, mv, bSink) // A non-Evan has no dragon to move.
	space.MoveDragon(1, 10000, 1, mv, aSink)
	if ops := bSink.opcodes(); len(ops) != 1 || ops[0] != protocol.SendMoveDragon {
		t.Fatalf("dragon move relay %x", ops)
	}
	if p := space.rooms[mapKey{channel: 1, mapID: 10000}][1].dragonPacket(); p[6] != 30 || p[10] != 40 {
		t.Fatalf("dragon position not tracked % x", p)
	}
	aSink.pkts, bSink.pkts = nil, nil
	space.RefreshDragon(1, 10000, 2, 2200) // Job change into Evan spawns one.
	if a, b := aSink.opcodes(), bSink.opcodes(); len(a) != 1 || a[0] != protocol.SendSpawnDragon || len(b) != 1 || b[0] != protocol.SendSpawnDragon {
		t.Fatalf("job change spawn %x %x", a, b)
	}
}

// A GM_HIDE member is despawned for peers but keeps seeing and following
// them; unhiding spawns it again.
func TestMapSpaceGMHide(t *testing.T) {
	m := newMapSpace(800)
	a, b := &gameplayTestSink{}, &gameplayTestSink{}
	m.Enter(1, 100, domain.Character{ID: 1, X: 0, Y: 0}, a)
	m.Enter(1, 100, domain.Character{ID: 2, X: 10, Y: 0}, b)
	if m.VisibleCount(1, 100, 1) != 1 {
		t.Fatal("setup")
	}
	m.SetHidden(1, 100, 1, true)
	if m.VisibleCount(1, 100, 1) != 0 || m.VisibleCount(1, 100, 2) != 0 {
		t.Fatal("hidden member still in a visible set")
	}
	before := len(a.packets)
	m.Move(1, 100, 2, domain.Character{ID: 2, X: 20, Y: 0}, 10, 0, []byte{0})
	if len(a.packets) == before {
		t.Fatal("hidden member stopped receiving peer movement")
	}
	bBefore := len(b.packets)
	m.Move(1, 100, 1, domain.Character{ID: 1, X: 5, Y: 0}, 0, 0, []byte{0})
	if len(b.packets) != bBefore {
		t.Fatal("peer received a hidden member's movement")
	}
	m.SetHidden(1, 100, 1, false)
	if m.VisibleCount(1, 100, 1) != 1 || m.VisibleCount(1, 100, 2) != 1 {
		t.Fatal("unhide did not restore visibility")
	}
}

func TestMapSpaceEnterWhileHidden(t *testing.T) {
	m := newMapSpace(800)
	a, b := &gameplayTestSink{}, &gameplayTestSink{}
	m.Enter(1, 100, domain.Character{ID: 2, X: 10, Y: 0}, b)
	hidden := domain.Character{ID: 1, SkillBuffs: []domain.SkillBuff{{ID: 9001004, Stat: "GM_HIDE", Value: 1, ExpiresAt: time.Now().Add(time.Minute)}}}
	m.Enter(1, 100, hidden, a)
	if len(b.packets) != 0 {
		t.Fatal("peer was told about a hidden entrant")
	}
	if len(a.packets) != 1 {
		t.Fatalf("hidden entrant must see the peer: %d packets", len(a.packets))
	}
}

// A job change re-spawns the actor for its viewers only: the v148 client
// takes a remote user's job from the spawn packet, never from CharacterLook.
func TestMapSpaceRespawnRefreshesViewersJob(t *testing.T) {
	space := newMapSpace(500)
	aSink, bSink, farSink := &memSink{}, &memSink{}, &memSink{}
	a := domain.Character{ID: 1, Name: "A", Job: 400, Level: 200, HP: 50, MaxHP: 50, MP: 50, MaxMP: 50, Revision: 1}
	b := domain.Character{ID: 2, Name: "B", Level: 1, HP: 50, MaxHP: 50, MP: 50, MaxMP: 50, X: 100, Revision: 1}
	far := domain.Character{ID: 3, Name: "C", Level: 1, HP: 50, MaxHP: 50, MP: 50, MaxMP: 50, X: 5000, Revision: 1}
	space.Enter(1, 10000, a, aSink)
	space.Enter(1, 10000, b, bSink)
	space.Enter(1, 10000, far, farSink)
	aSink.pkts, bSink.pkts, farSink.pkts = nil, nil, nil
	a.Job = 411
	space.publication.Lock()
	space.RespawnLocked(1, 10000, 1, a)
	space.publication.Unlock()
	if got := bSink.opcodes(); len(got) != 2 || got[0] != protocol.SendRemovePlayer || got[1] != protocol.SendSpawnPlayer {
		t.Fatalf("viewer packets %v", got)
	}
	if want := protocol.SpawnPlayer(a, 4); string(bSink.pkts[1]) != string(want) {
		t.Fatal("viewer did not receive the spawn with the new job")
	}
	if len(aSink.pkts) != 0 || len(farSink.pkts) != 0 {
		t.Fatalf("actor or out-of-range member was told: %v %v", aSink.opcodes(), farSink.opcodes())
	}
	if space.VisibleCount(1, 10000, 1) != 1 || space.VisibleCount(1, 10000, 2) != 1 {
		t.Fatal("visibility changed")
	}
}
