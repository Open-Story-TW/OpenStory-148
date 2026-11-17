package protocol

import (
	"encoding/hex"
	"testing"

	"maple-go-server/internal/content"
)

func TestV148MobHitEscort(t *testing.T) {
	body := []byte{0xf1, 0x42, 0x0f, 0x00, 0x39, 0x04, 0x00, 0x00, 0xf8, 0x42, 0x0f, 0x00}
	hit, err := ReadMobHitEscort(NewReader(body))
	if err != nil || hit != (MobHitEscort{Attacker: 1000177, CharacterID: 1081, Escort: 1000184}) {
		t.Fatalf("hit = %+v, err = %v", hit, err)
	}
	if _, err = ReadMobHitEscort(NewReader(body[:8])); err == nil {
		t.Fatal("old two-object body was accepted")
	}
	if got := hex.EncodeToString(DamageEscort(hit.Escort, 44, 856, 900)); got != "b501f8420f00012c0000005803000084030000" {
		t.Fatalf("damage reply = %s", got)
	}
}

func TestV148EscortPathIncludesStopAttributeAndResumeIndex(t *testing.T) {
	nodes := []content.EscortNode{
		{X: -40, Y: 20},
		{X: 10, Y: 30, Attribute: 2, StopMillis: 1000},
	}
	want := "bf010100000002000000d8ffffff14000000d8ffffff14000000000000000a0000001e00000002000000e8030000010000000000"
	if got := hex.EncodeToString(EscortPath(1, -40, 20, 1, nodes, 0)); got != want {
		t.Fatalf("escort path = %s, want %s", got, want)
	}
	want = want[:len(want)-4] + "01fa00000000"
	if got := hex.EncodeToString(EscortPath(1, -40, 20, 1, nodes, 250)); got != want {
		t.Fatalf("paused escort path = %s, want %s", got, want)
	}
	if got := hex.EncodeToString(ResumeEscort(1)); got != "c00101000000" {
		t.Fatalf("resume escort = %s", got)
	}
}

func TestV148EscortAttack(t *testing.T) {
	body, err := hex.DecodeString("1c020000c60200008403000000ff1a0000000189f83f00")
	if err != nil {
		t.Fatal(err)
	}
	hit, err := ReadEscortAttack(NewReader(body))
	if err != nil || hit != (MobHitEscort{Attacker: 540, CharacterID: 710, Escort: 900}) {
		t.Fatalf("hit = %+v, err = %v", hit, err)
	}
	if _, err := ReadEscortAttack(NewReader(body[:len(body)-1])); err == nil {
		t.Fatal("truncated escort attack was accepted")
	}
}
