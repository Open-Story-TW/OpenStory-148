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
