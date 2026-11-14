package content

import (
	"os"
	"testing"
)

// The profession data comes from the audited WZ export; without one these
// definitions do not exist and the feature stays off.
func professionRoot(t *testing.T) string {
	root := os.Getenv("MAPLEGO_WZ_ROOT")
	if root == "" {
		t.Skip("MAPLEGO_WZ_ROOT is not set")
	}
	return root
}

func TestProfessionRules(t *testing.T) {
	if !ValidProfession(ProfessionAlchemy) || ValidProfession(92050000) || ValidProfession(0) {
		t.Fatal("profession IDs")
	}
	if ProfessionRequires(ProfessionAlchemy) != ProfessionHerbalism || ProfessionRequires(ProfessionSmithing) != ProfessionMining || ProfessionRequires(ProfessionAccessory) != ProfessionMining || ProfessionRequires(ProfessionMining) != 0 {
		t.Fatal("craft prerequisites")
	}
	if ProfessionExpToNext("", ProfessionMining, 1) != 250 || ProfessionExpToNext("", ProfessionMining, 9) != 5850 || ProfessionExpToNext("", ProfessionMining, 10) != 0 || ProfessionExpToNext("", 1000, 1) != 0 {
		t.Fatal("proficiency table")
	}
	if _, err := ProfessionRecipe("", 92000002); err == nil {
		t.Fatal("a recipe exists without WZ data")
	}
}

func TestProfessionWZData(t *testing.T) {
	root := professionRoot(t)
	r, err := ProfessionRecipe(root, 92000002)
	if err != nil || r.Profession != ProfessionHerbalism || r.Level != 1 || r.Proficiency != 20 || r.Fatigue != 1 || r.Learned || len(r.Products) != 1 || r.Products[0] != (RecipeItem{Item: 4023000, Count: 1, Weight: 100}) || len(r.Ingredients) != 2 || r.Ingredients[0] != (RecipeItem{Item: 4022000, Count: 2}) {
		t.Fatalf("herb oil recipe: %+v %v", r, err)
	}
	if r, err = ProfessionRecipe(root, 92020031); err != nil || !r.Learned || r.Level != 7 {
		t.Fatalf("learned smithing recipe: %+v %v", r, err)
	}
	if _, err = ProfessionRecipe(root, 92009999); err == nil {
		t.Fatal("unknown recipe accepted")
	}
	if ProfessionExpToNext(root, ProfessionHerbalism, 3) != 1050 {
		t.Fatal("WZ proficiency table")
	}
	node, err := GatherNodeDefinition(root, 200003)
	if err != nil || node.Profession != ProfessionMining || node.Level != 4 || node.Free {
		t.Fatalf("vein: %+v %v", node, err)
	}
	yield, err := GatherYield(root, node)
	if err != nil || len(yield) != 2 || yield[0] != 4010003 || yield[1] != 4010000 {
		t.Fatalf("vein yield: %v %v", yield, err)
	}
	if node, err = GatherNodeDefinition(root, 100011); err != nil || !node.Free {
		t.Fatalf("rare herb: %+v %v", node, err)
	}
	if yield, err = GatherYield(root, node); err != nil || len(yield) != 23 {
		t.Fatalf("rare herb yield: %d %v", len(yield), err)
	}
	if _, err = GatherNodeDefinition(root, 1002000); err == nil {
		t.Fatal("an ordinary reactor is a gathering node")
	}
	reactors, err := MapReactors(root, 910001005)
	if err != nil || len(reactors) != 20 {
		t.Fatalf("secret mine reactors: %d %v", len(reactors), err)
	}
	if reactors, err = MapReactors(root, 910001000); err != nil || len(reactors) != 0 {
		t.Fatalf("town reactors: %d %v", len(reactors), err)
	}
}
