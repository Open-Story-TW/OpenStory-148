package all

import (
	"testing"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/skills"
)

// Every handler must be registered from the job package that owns its ID.
func TestRegisteredSkillsLiveInTheirOwnerPackage(t *testing.T) {
	ids := skills.IDs()
	if len(ids) == 0 {
		t.Fatal("no skills registered")
	}
	for _, id := range ids {
		h, _ := skills.Lookup(id)
		if owner := skills.OwnerOf(id); owner == "" || h.Package() != owner {
			t.Errorf("skill %d registered by %q, owned by %q", id, h.Package(), owner)
		}
		if _, err := content.SkillDefinition("", id); err != nil {
			t.Errorf("skill %d not in the WZ seed: %v", id, err)
		}
	}
}

func cast(t *testing.T, id, level int) skills.CastPlan {
	t.Helper()
	h, ok := skills.Lookup(id)
	if !ok || h.Cast == nil {
		t.Fatalf("skill %d not castable", id)
	}
	def, err := content.SkillDefinition("", id)
	if err != nil {
		t.Fatal(err)
	}
	values, err := def.Values(level)
	if err != nil {
		t.Fatal(err)
	}
	p, err := h.Cast(&skills.CastContext{Skill: def, Level: level, Values: values, Now: time.Unix(0, 0)})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// The registered reference handlers reproduce the pre-registry CastSkill.
func TestReferenceHandlersMatchLegacyCast(t *testing.T) {
	p := cast(t, 1001003, 1)
	if p.MPCost != 8 || p.Self == nil || len(p.Self.Stats) != 1 || p.Self.Stats[0] != (skills.StatValue{Stat: "WDEF", Value: 10}) || p.Self.Duration != 110*time.Second || p.Party != nil {
		t.Fatalf("iron body %+v %+v", p, p.Self)
	}
	p = cast(t, 1002, 1)
	if p.Self == nil || len(p.Self.Stats) != 1 || p.Self.Stats[0].Stat != "SPEED" || p.Self.Tick != 0 {
		t.Fatalf("nimble feet %+v", p.Self)
	}
	p = cast(t, 1001, 1)
	if p.Self == nil || len(p.Self.Stats) != 1 || p.Self.Stats[0].Stat != "RECOVERY" || p.Self.Tick != 5*time.Second {
		t.Fatalf("recovery %+v", p.Self)
	}
	if h, _ := skills.Lookup(1001005); h.Attack == nil || h.Cast != nil {
		t.Fatal("slash blast is an attack")
	}
}

func TestRegisterRejectsDuplicates(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("duplicate registration accepted")
		}
	}()
	skills.Register(skills.Handler{ID: 1001003, Kind: skills.KindBuff, Cast: skills.StandardBuff})
}
