package content

import (
	"os"
	"path/filepath"
	"testing"
)

func writeSkillBook(t *testing.T, root, book, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, "Skill.wz"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Skill.wz", book), []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
}

// A data root without a job's book gets the built-in v148 one: Blast 1221009
// has four lines there (three in v145).
func TestSkillDefinitionUsesBuiltInV148Book(t *testing.T) {
	blast, err := SkillDefinition(t.TempDir(), 1221009)
	if err != nil {
		t.Fatal(err)
	}
	v, err := blast.Values(blast.MaxLevel)
	if err != nil || v["attackCount"] != 4 {
		t.Fatalf("blast lines %v %v", v["attackCount"], err)
	}
	// A job v145 does not have at all.
	if _, err = SkillDefinition(t.TempDir(), 5081001); err != nil {
		t.Fatalf("a v148-only book: %v", err)
	}
}

// A book the operator wrote is the only one read: its values win and a skill
// it lacks is unknown, even though the built-in book has it.
func TestSkillDefinitionKeepsOperatorBook(t *testing.T) {
	root := t.TempDir()
	writeSkillBook(t, root, "122.img.xml", `<imgdir name="122.img"><imgdir name="skill"><imgdir name="1221009"><imgdir name="common"><int name="maxLevel" value="30"/><int name="attackCount" value="9"/><int name="damage" value="100"/></imgdir></imgdir></imgdir></imgdir>`)
	blast, err := SkillDefinition(root, 1221009)
	if err != nil {
		t.Fatal(err)
	}
	if v, err := blast.Values(30); err != nil || v["attackCount"] != 9 {
		t.Fatalf("operator value %v %v", v["attackCount"], err)
	}
	if _, err = SkillDefinition(root, 1221000); err == nil {
		t.Fatal("a skill the operator's book lacks came from the built-in book")
	}
}

// With the real v145 data root the stock file gives way to v148, and a skill
// v148 dropped (Octopus 5211001) is still read from the stock file.
func TestSkillDefinitionReplacesStockV145Book(t *testing.T) {
	root := os.Getenv("MAPLEGO_TEST_WZ_ROOT")
	if root == "" {
		t.Skip("MAPLEGO_TEST_WZ_ROOT not set")
	}
	blast, err := SkillDefinition(root, 1221009)
	if err != nil {
		t.Fatal(err)
	}
	if v, err := blast.Values(blast.MaxLevel); err != nil || v["attackCount"] != 4 {
		t.Fatalf("stock book was not replaced: %v %v", v["attackCount"], err)
	}
	if _, err = SkillDefinition(root, 5211001); err != nil {
		t.Fatalf("a skill only v145 has: %v", err)
	}
}
