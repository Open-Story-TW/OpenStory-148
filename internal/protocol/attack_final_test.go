package protocol

import (
	"encoding/hex"
	"testing"
)

// Frames the v148 client sent (2026-11-15). A final attack names the skill it
// follows and adds one byte before the targets; the parser used to read that
// byte as the first byte of the first target.
func TestReadAttackMeleeFinalAttackFollowsSkill(t *testing.T) {
	for _, c := range []struct {
		name, frame            string
		skill                  int
		follows                int32
		targets, lines, damage int
		x                      int16
	}{
		{"advanced final attack after Power Strike", "01120d1711001e258be6150012000c96ac0b0104f8972101000000002c460f000041420f00070100706f98000115ffd70015ffd7008f01203300004e3000005d2b923e740ecef115ffd700", 1120013, 1001004, 1, 2, 13088, -235},
		{"page final attack after Power Strike", "0111824f1200140753da6e0012800c96ac0b010412482a01000000002c460f00004d430f00070100706f98000193ffd70093ffd7008f012a3200005d2b923e8bbb0701dcffd700", 1200002, 1001004, 1, 1, 12842, -36},
		{"spearman final attack", "011122d61300140753da6e000f000c96ac0b0204ccaa2f0100000000190114000094430f00070100706f98000135ffd70035ffd7008f025ec000005d2b923eb086766315ffd700", 1300002, 1311001, 1, 1, 49246, -235},
		{"four targets after Slash Blast", "0141824f1200140753da6e0108800c96ac0b01045aa62901000000002d460f000019430f00078100706f98000125ffd70025ffd7008f011c1800005d2b923eae12a84618430f00070100706f98000105ffd70005ffd700d5014c0800005d2b923e1b61d7ae17430f00070100706f980001e5fed700e5fed7001b028f0100005d2b923e11bf951016430f00070100706f980001c5fed700c5fed7006102fd0000005d2b923e9afdc4e013ffd700", 1200002, 1001005, 4, 1, 6172, -237},
		{"no targets", "01020d1711001e258be6150012800c96ac0b0102fa942201000000002c460f000011ffd700", 1120013, 1001004, 0, 2, 0, -239},
	} {
		body, err := hex.DecodeString(c.frame)
		if err != nil {
			t.Fatal(err)
		}
		a, err := ReadAttack(RecvMeleeAttack, NewReader(body))
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if a.SkillID != c.skill || a.Follows != c.follows || len(a.Targets) != c.targets || a.LineCount() != c.lines || a.X != c.x || a.Y != 215 {
			t.Fatalf("%s: skill %d follows %d targets %d lines %d at (%d,%d)", c.name, a.SkillID, a.Follows, len(a.Targets), a.LineCount(), a.X, a.Y)
		}
		if c.targets > 0 && a.Targets[0].Lines[0].Damage != c.damage {
			t.Fatalf("%s: first line %d, want %d", c.name, a.Targets[0].Lines[0].Damage, c.damage)
		}
	}
}

// Corkscrew Blow as the v148 client sent it (2026-11-15): the plain melee
// header, no bytes between it and the targets.
func TestReadAttackCorkscrewBlowHasNoExtraHeader(t *testing.T) {
	body, _ := hex.DecodeString("0121ccd54d00144980353c007080e94e699b0805ff3f3301000000000000000046420f00078100706f980001e5fed700e5fed7003102bd3c00005d2b923e0db3ec3e45420f00070100706f980001c5fed700c5fed7007702fe3100005d2b923e9afdc4e0c1fed700")
	a, err := ReadAttack(RecvMeleeAttack, NewReader(body))
	if err != nil || a.SkillID != 5101004 || len(a.Targets) != 2 || a.LineCount() != 1 || a.Targets[0].Lines[0].Damage != 15549 || a.Follows != 0 {
		t.Fatalf("%+v %v", a, err)
	}
}

// 5111012 names action 0x1ab, above the last v145 action.
func TestReadAttackAcceptsV148Actions(t *testing.T) {
	body, _ := hex.DecodeString("0111e4fc4d00144c2a972000ab01e8c0782908050005910600000000000000004a420f00070100706f98000125ffd70025ffd7006603930c01005d2b923ec74950843dffd700")
	a, err := ReadAttack(RecvMeleeAttack, NewReader(body))
	if err != nil || a.SkillID != 5111012 || a.Display&0x7fff != 0x1ab || len(a.Targets) != 1 || a.Targets[0].Lines[0].Damage != 68755 {
		t.Fatalf("%+v %v", a, err)
	}
}

// Poison Bomb's explosion as the v148 client sent it (2026-11-20): the
// charge header, two targets, the attacker position and then the bomb's.
func TestReadAttackPoisonBombExplosionPoint(t *testing.T) {
	body, _ := hex.DecodeString("01211e51d7001e515b81d4000000000011803afe99de07061090380d000000000000000012430f00070100706f9800011900d7001900d70078001c0100005d2b923e78bf979611430f00070100706f980001f9ffd700f9ffd700be00c00000005d2b923e30c535961101d70098ffd700")
	a, err := ReadAttack(RecvMeleeAttack, NewReader(body))
	if err != nil || a.SkillID != 14111006 || a.Charge != 212 || len(a.Targets) != 2 || a.LineCount() != 1 {
		t.Fatalf("%+v %v", a, err)
	}
	if a.Targets[0].Lines[0].Damage != 284 || a.Targets[1].Lines[0].Damage != 192 {
		t.Fatalf("lines %d %d", a.Targets[0].Lines[0].Damage, a.Targets[1].Lines[0].Damage)
	}
	if a.X != 273 || a.Y != 215 || a.TailX != -104 || a.TailY != 215 {
		t.Fatalf("attacker (%d,%d) bomb (%d,%d)", a.X, a.Y, a.TailX, a.TailY)
	}
}
