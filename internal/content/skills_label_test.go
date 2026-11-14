package content

import "testing"

func TestValuesSkipStringAttributes(t *testing.T) {
	def, err := SkillDefinition("", 4311003)
	if err != nil {
		t.Fatal(err)
	}
	v, err := def.Values(20)
	if err != nil || v["damage"] != 190 || v["attackCount"] != 2 {
		t.Fatal(v, err)
	}
	if _, ok := v["action"]; ok {
		t.Fatal("action parsed as a value")
	}
}
