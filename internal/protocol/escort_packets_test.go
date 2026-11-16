package protocol

import (
	"encoding/hex"
	"testing"
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
