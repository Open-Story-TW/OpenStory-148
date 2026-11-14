package world

import (
	"bytes"
	"context"
	"testing"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

// The native OnHit path has already applied 86EBBE before encoding this
// ordinary contact frame. This regression proves final damage crosses the
// strict ingress, HP mutation and foreign publication exactly once. It does
// not simulate a client proc or replace the required real-client acceptance.
func TestNativePotentialContactFinalDamageAndReplay(t *testing.T) {
	s, lives, sinks, mob := sharedWorldFixture(t)
	s.skills = newSkillSpace()
	for i := range lives {
		if err := s.enterSkills(context.Background(), 1, lives[i], sinks[i]); err != nil {
			t.Fatal(err)
		}
	}
	c := &lives[0].session.Character
	c.X, c.Y = mob.life.X, mob.life.Y
	c.Equipment = append(c.Equipment, domain.Equipment{ID: 86996, ItemID: 1002019, Slot: -1, WDEF: 8,
		Potential: domain.EquipmentPotential{Present: true, RawState: 18, Flags: 65535, Options: [5]uint16{20353, 10014}}})
	damage := min(5, max(1, mob.stats.PAD*2))
	q := protocol.PlayerDamage{Tick: 123, Type: -1, Damage: damage, MonsterID: mob.stats.ID, ObjectID: mob.objectID}
	w := protocol.NewRawWriter()
	w.U32(0)
	w.U32(q.Tick)
	w.U8(255)
	w.U8(0)
	w.I32(int32(q.Damage))
	w.U16(0)
	w.I32(int32(q.MonsterID))
	w.I32(q.ObjectID)
	w.U8(0)
	w.I32(0)
	w.I32(0)
	w.U8(0)
	w.U8(0)
	w.U8(0)
	body := w.Packet()
	beforeHP, beforeMP := c.HP, c.MP
	for attempt := 0; attempt < 2; attempt++ {
		handled, err := s.handleSkills(context.Background(), lives[0], sinks[0], protocol.RecvTakeDamage, protocol.NewReader(body))
		if err != nil || !handled {
			t.Fatalf("native final-damage ingress failed: %v %v", handled, err)
		}
		if c.HP != beforeHP-damage || c.MP != beforeMP || !lives[0].dirty {
			t.Fatalf("damage reduced again or replayed: HP=%d MP=%d", c.HP, c.MP)
		}
	}
	if sinks[1].count(0x13b) != 1 {
		t.Fatal("foreign damage was lost or replayed")
	}
	want := protocol.PlayerDamagePacket(c.ID, q)
	found := false
	for _, packet := range sinks[1].packets {
		if bytes.Equal(packet, want) {
			found = true
		}
	}
	if !found {
		t.Fatal("foreign publication changed native final damage")
	}
}
