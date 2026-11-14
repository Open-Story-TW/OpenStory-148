package content

import "testing"

func TestSkillFormulaBoundedArithmetic(t *testing.T) {
	for _, c := range []struct {
		s       string
		l, want int
	}{{"3+u(x/2)", 3, 5}, {"6+2*d(x/5)", 7, 8}, {"-30+3*x", 7, -9}, {"=(10+x)*2", 3, 26}, {"d(-1.2)", 1, -2}} {
		v, e := SkillFormula(c.s, c.l)
		if e != nil || v != c.want {
			t.Fatalf("%s=%d %v", c.s, v, e)
		}
	}
	for _, s := range []string{"os.execute('x')", "1/0", "x^2", "1e9", "u(x", "NaN", "(1+2))"} {
		if _, e := SkillFormula(s, 1); e == nil {
			t.Fatalf("accepted%s", s)
		}
	}
}
func TestBeginnerSkillWZValues(t *testing.T) {
	for _, id := range []int{1000, 1001, 1002} {
		s, e := SkillDefinition("", id)
		if e != nil || s.MaxLevel != 3 || !s.CanLearnAdventurer(0) || s.CanLearnAdventurer(2000) {
			t.Fatalf("skill%d %+v %v", id, s, e)
		}
	}
	s, _ := SkillDefinition("", 1001)
	v, e := s.Values(3)
	if e != nil || v["mpCon"] != 15 || v["x"] != 12 || v["time"] != 30 || v["cooltime"] != 120 {
		t.Fatalf("recovery%+v %v", v, e)
	}
	s, _ = SkillDefinition("", 1002)
	v, e = s.Values(2)
	if e != nil || v["speed"] != 15 || v["time"] != 8 {
		t.Fatalf("speed%+v %v", v, e)
	}
	s, _ = SkillDefinition("", 1101004)
	if !s.CanLearnAdventurer(112) || s.CanLearnAdventurer(122) || s.CanLearnAdventurer(100) {
		t.Fatal("advanced job ancestry")
	}
}
