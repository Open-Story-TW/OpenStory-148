package buffstat

import "testing"

func TestTableOrdersAreUniqueAndDense(t *testing.T) {
	local, remote := map[int]Stat{}, map[int]Stat{}
	for _, s := range All() {
		if s.Order > 0 {
			if prior, dup := local[s.Order]; dup {
				t.Fatalf("local order %d shared by %s and %s", s.Order, prior, s.Name)
			}
			local[s.Order] = s.Name
		}
		if s.RemoteOrder > 0 {
			if prior, dup := remote[s.RemoteOrder]; dup {
				t.Fatalf("remote order %d shared by %s and %s", s.RemoteOrder, prior, s.Name)
			}
			remote[s.RemoteOrder] = s.Name
		}
		if (s.TwoState != 0) != (s.Bit >= 248) {
			t.Fatalf("two-state layout on %s", s.Name)
		}
	}
	for i := 1; i <= len(local); i++ {
		if _, ok := local[i]; !ok {
			t.Fatalf("local order gap at %d", i)
		}
	}
	if len(local) != 190 || len(remote) != 72 {
		t.Fatalf("native audit has 190 triplets and 72 remote stats, got %d/%d", len(local), len(remote))
	}
}

// Bits proven by earlier native work must agree with the generated table.
func TestTableMatchesReviewedBits(t *testing.T) {
	for name, bit := range map[Stat]int{"WDEF": 1, "SPEED": 7, "MAXHP": 13, "MAXMP": 14, "RECOVERY": 34, "MAPLE_WARRIOR": 35, "MONSTER_RIDING": 251} {
		s, ok := Lookup(name)
		if !ok || s.Bit != bit {
			t.Fatalf("%s: %+v", name, s)
		}
	}
	speed, _ := Lookup("SPEED")
	if !speed.MoveTail || len(speed.Remote) != 1 || speed.Remote[0] != ValueU8 {
		t.Fatalf("speed native layout %+v", speed)
	}
	recovery, _ := Lookup("RECOVERY")
	if recovery.RemoteOrder != 0 {
		t.Fatal("remote clients do not decode recovery")
	}
	if alias, _ := Lookup("HANDS"); alias.Name != "AVOID" {
		t.Fatalf("alias resolution %+v", alias)
	}
}

func TestResolveMaskWidthAndRejection(t *testing.T) {
	specs, err := Resolve([]Stat{"MAXMP", "MAXHP"})
	if err != nil {
		t.Fatal(err)
	}
	if m := Mask(specs); m[7] != 1<<13|1<<14 || m[0] != 0 {
		t.Fatalf("mask %x", m)
	}
	if got := LocalTriplets(specs); got[0].Name != "MAXHP" || got[1].Name != "MAXMP" {
		t.Fatalf("client order %v", got)
	}
	if IntValues(specs) || HasMoveTail(specs) {
		t.Fatal("hyper body is I16 without movement byte")
	}
	surge, _ := Resolve([]Stat{"WATK", "SPIRIT_SURGE"})
	if !IntValues(surge) {
		t.Fatal("SPIRIT_SURGE forces I32 values")
	}
	for _, bad := range [][]Stat{{"NOPE"}, {"WDEF", "WDEF"}, {"AVOID", "HANDS"}} {
		if _, err := Resolve(bad); err == nil {
			t.Fatalf("%v accepted", bad)
		}
	}
}

func TestAbnormalStatsExistInTable(t *testing.T) {
	for name := range abnormal {
		if _, ok := Lookup(name); !ok {
			t.Errorf("abnormal stat %s missing from the table", name)
		}
	}
}

func TestStackedForms(t *testing.T) {
	for from, to := range map[Stat]Stat{"WATK": "ANGEL_ATK", "MATK": "ANGEL_MATK", "ACC": "ANGEL_ACC", "AVOID": "ANGEL_AVOID", "SPEED": "ANGEL_SPEED", "JUMP": "ANGEL_JUMP", "WDEF": "WDEF", "BOOSTER": "BOOSTER"} {
		if got := StackedForm(from); got != to {
			t.Fatalf("%s -> %s, want %s", from, got, to)
		}
		if Stackable(StackedForm(from)) != (from != to) {
			t.Fatalf("%s stackable %v", to, Stackable(to))
		}
	}
	if !Stackable("HP_BOOST") || Stackable("WATK") {
		t.Fatal("indie stats only")
	}
}
