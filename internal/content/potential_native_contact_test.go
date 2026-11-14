package content

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"maple-go-server/internal/domain"
)

func nativeContactPotentialFixture(t *testing.T, fields string) string {
	t.Helper()
	root := effectsFixture(t, 5)
	if err := os.MkdirAll(filepath.Join(root, "Character.wz/Cap"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Character.wz/Cap/01002019.img.xml"), []byte(`<imgdir name="hat"><imgdir name="info"><int name="reqLevel" value="10"/></imgdir></imgdir>`), 0644); err != nil {
		t.Fatal(err)
	}
	xml := `<imgdir name="ItemOption.img">`
	for _, row := range []struct {
		id     int
		fields string
	}{
		{20353, fields}, {10014, `<int name="incMDD" value="10"/>`},
		{20354, `<int name="HP" value="5"/><int name="prop" value="30"/>`},
	} {
		xml += fmt.Sprintf(`<imgdir name="%06d"><imgdir name="info"><int name="weight" value="1"/></imgdir><imgdir name="level"><imgdir name="1">%s</imgdir></imgdir></imgdir>`, row.id, row.fields)
	}
	xml += `</imgdir>`
	if err := os.WriteFile(filepath.Join(root, "Item.wz/ItemOption.img.xml"), []byte(xml), 0644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestNativeContactPotentialResolvesActualHatAndKeepsOtherEventsPending(t *testing.T) {
	root := nativeContactPotentialFixture(t, `<int name="prop" value="30"/><int name="ignoreDAM" value="7"/>`)
	now := time.Unix(1800000000, 0)
	e := domain.Equipment{ID: 86996, ItemID: 1002019, Slot: -1, Potential: domain.EquipmentPotential{
		Present: true, RawState: 18, Options: [5]uint16{20353, 10014}, Flags: 65535,
	}}
	resolve := func(e domain.Equipment) PotentialEffects {
		t.Helper()
		v, err := ResolvePotentialEffects(root, domain.Character{Equipment: []domain.Equipment{e}}, now)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	v := resolve(e)
	if len(v.Pending) != 0 || len(v.NativeContactDamage) != 1 || v.Values["incMDD"] != 10 {
		t.Fatalf("actual sealed/revealed hat did not resolve: %+v", v)
	}
	d := v.NativeContactDamage[0]
	if d.Prop != 30 || d.IgnoreDAM != 7 || d.Source.EquipmentID != 86996 || d.Source.Band != 1 || d.Source.Line != 1 || d.Source.OptionID != 20353 {
		t.Fatalf("native source identity or band lost: %+v", d)
	}
	// Two same-prop lines stay separate, rather than inventing 60% chance.
	e.Potential.Options = [5]uint16{20353, 20353, 20354}
	v = resolve(e)
	if len(v.NativeContactDamage) != 2 || v.NativeContactDamage[0].Prop != 30 || v.NativeContactDamage[1].Prop != 30 ||
		len(v.Pending) != 1 || v.Pending[0].OptionID != 20354 {
		t.Fatalf("native lines combined or unrelated proc admitted: %+v", v)
	}
	e.Potential.Options = [5]uint16{20353}
	for name, change := range map[string]func(*domain.Equipment){
		"bag":     func(e *domain.Equipment) { e.Slot = 2 },
		"hidden":  func(e *domain.Equipment) { e.Potential.RawState = 1 },
		"cash":    func(e *domain.Equipment) { e.CashID = 123 },
		"expired": func(e *domain.Equipment) { e.ExpiresAt = now.UnixMilli() },
	} {
		t.Run(name, func(t *testing.T) {
			copy := e
			change(&copy)
			got := resolve(copy)
			if len(got.NativeContactDamage) != 0 || len(got.Lines) != 0 {
				t.Fatalf("inactive source admitted: %+v", got)
			}
		})
	}
	// The source native scan does not consume the weapon slot.
	e.ItemID, e.Slot = 1302000, -11
	v = resolve(e)
	if len(v.NativeContactDamage) != 0 || len(v.Pending) != 1 {
		t.Fatalf("unscanned slot admitted: %+v", v)
	}
	if PotentialAttributeSupport()["prop"] != PotentialPending || PotentialAttributeSupport()["ignoreDAM"] != PotentialPending {
		t.Fatal("conditional recognition changed global support")
	}
}

func TestNativeContactPotentialDoesNotAdmitOtherProcShapes(t *testing.T) {
	for name, fields := range map[string]string{
		"zero prop":        `<int name="prop" value="0"/><int name="ignoreDAM" value="7"/>`,
		"excess prop":      `<int name="prop" value="101"/><int name="ignoreDAM" value="7"/>`,
		"negative amount":  `<int name="prop" value="30"/><int name="ignoreDAM" value="-7"/>`,
		"missing prop":     `<int name="ignoreDAM" value="7"/>`,
		"percentage":       `<int name="prop" value="30"/><int name="ignoreDAMr" value="7"/>`,
		"additional event": `<int name="prop" value="30"/><int name="ignoreDAM" value="7"/><int name="time" value="2"/>`,
		"reflect":          `<int name="prop" value="30"/><int name="DAMreflect" value="7"/>`,
	} {
		t.Run(name, func(t *testing.T) {
			root := nativeContactPotentialFixture(t, fields)
			e := domain.Equipment{ID: 1, ItemID: 1002019, Slot: -1, Potential: domain.EquipmentPotential{Present: true, RawState: 18, Options: [5]uint16{20353}}}
			v, err := ResolvePotentialEffects(root, domain.Character{Equipment: []domain.Equipment{e}}, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if len(v.NativeContactDamage) != 0 || len(v.Pending) != 1 {
				t.Fatalf("unreviewed shape admitted: %+v", v)
			}
		})
	}
}
