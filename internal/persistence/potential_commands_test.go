package persistence

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
)

func potentialCommandUnitFixture() (domain.Character, domain.PotentialCommandRequest) {
	c := domain.Character{ID: 99, AccountID: 88, WorldID: 2, Revision: 4, HP: 50, MP: 20, MaxHP: 100, MaxMP: 40, BaseMaxHP: 100, BaseMaxMP: 40, VitalsModel: 1, Mesos: 100000,
		Items:     []domain.InventoryItem{{ID: 101, InvType: 2, Slot: 2, TemplateID: 2049400, Quantity: 2}},
		Equipment: []domain.Equipment{{ID: 201, ItemID: 1322005, Slot: -11, UpgradeSlots: 7}}}
	r := domain.PotentialCommandRequest{Session: domain.CharacterSession{Character: c, CharacterID: c.ID, AccountID: c.AccountID, SessionID: "potential-test-session", Generation: 3,
		ChannelID: 1, Lease: domain.WorldLease{WorldID: 2, InstanceID: "potential-world", Epoch: 1}}, ClientTick: 41, Action: domain.PotentialSeal, SourceSlot: 2, TargetSlot: -11}
	return c, r
}

func writePotentialCommandTestWZ(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, dir := range []string{"Character.wz/Weapon", "Item.wz/Consume"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		"Character.wz/Weapon/01322005.img.xml": `<imgdir name="01322005.img"><imgdir name="info"><int name="reqLevel" value="10"/><int name="tuc" value="7"/></imgdir></imgdir>`,
		"Item.wz/Consume/0204.img.xml":         `<imgdir name="0204.img"><imgdir name="02049400"><imgdir name="info"><int name="slotMax" value="100"/></imgdir></imgdir><imgdir name="02049700"><imgdir name="info"><int name="success" value="100"/></imgdir></imgdir><imgdir name="02049701"><imgdir name="info"><int name="success" value="80"/><int name="cursed" value="20"/></imgdir></imgdir></imgdir>`,
		"Item.wz/Consume/0246.img.xml":         `<imgdir name="0246.img"><imgdir name="02460000"><imgdir name="info"><int name="slotMax" value="100"/></imgdir></imgdir></imgdir>`,
	}
	var options strings.Builder
	options.WriteString(`<imgdir name="ItemOption.img">`)
	for _, id := range []int{10001, 20001, 30001, 40001} {
		fmt.Fprintf(&options, `<imgdir name="%06d"><imgdir name="info"><int name="weight" value="5"/></imgdir><imgdir name="level">`, id)
		for band := 1; band <= 20; band++ {
			fmt.Fprintf(&options, `<imgdir name="%d"><int name="incSTR" value="%d"/></imgdir>`, band, band)
		}
		options.WriteString(`</imgdir></imgdir>`)
	}
	options.WriteString(`</imgdir>`)
	files["Item.wz/ItemOption.img.xml"] = options.String()
	for file, data := range files {
		if err := os.WriteFile(filepath.Join(root, file), []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestPotentialCommandIdentityAndFamilyIsolation(t *testing.T) {
	_, r := potentialCommandUnitFixture()
	if !validPotentialCommand(r) {
		t.Fatal("valid scoped native action rejected")
	}
	if potentialRequestHash(r) == scrollRequestHash(domain.ScrollRequest{SourceSlot: r.SourceSlot, TargetSlot: r.TargetSlot}) {
		t.Fatal("potential receipt could be confused with ordinary scroll")
	}
	for _, mutate := range []func(*domain.PotentialCommandRequest){
		func(r *domain.PotentialCommandRequest) { r.Action = domain.PotentialMagnify },
		func(r *domain.PotentialCommandRequest) { r.SourceSlot++ },
		func(r *domain.PotentialCommandRequest) { r.TargetSlot-- },
		func(r *domain.PotentialCommandRequest) { r.LegendarySpirit = true },
	} {
		changed := r
		mutate(&changed)
		if potentialRequestHash(changed) == potentialRequestHash(r) {
			t.Fatal("changed native payload reused hash")
		}
	}
	for _, mutate := range []func(*domain.PotentialCommandRequest){
		func(r *domain.PotentialCommandRequest) { r.ClientTick++ },
		func(r *domain.PotentialCommandRequest) { r.Session.Generation++ },
		func(r *domain.PotentialCommandRequest) { r.Session.SessionID += "-next" },
		func(r *domain.PotentialCommandRequest) { r.Session.CharacterID++ },
		func(r *domain.PotentialCommandRequest) { r.Session.Lease.WorldID++ },
		func(r *domain.PotentialCommandRequest) { r.Session.AccountID++ },
	} {
		changed := r
		mutate(&changed)
		if potentialOperationID(changed) == potentialOperationID(r) {
			t.Fatal("ledger operation crossed an owner/tick scope")
		}
	}
	for _, tick := range []uint32{0, ^uint32(0)} {
		x := r
		x.ClientTick = tick
		if !validPotentialCommand(x) {
			t.Fatal("valid native U32 tick rejected", tick)
		}
	}
}

func TestPotentialCommandInvalidRequestsNeverReachSQL(t *testing.T) {
	_, r := potentialCommandUnitFixture()
	for name, mutate := range map[string]func(*domain.PotentialCommandRequest){
		"unknown action":             func(r *domain.PotentialCommandRequest) { r.Action = "cube" },
		"trait127":                   func(r *domain.PotentialCommandRequest) { r.SourceSlot = 127 },
		"zero source":                func(r *domain.PotentialCommandRequest) { r.SourceSlot = 0 },
		"zero target":                func(r *domain.PotentialCommandRequest) { r.TargetSlot = 0 },
		"wrong worn slot":            func(r *domain.PotentialCommandRequest) { r.TargetSlot = -200 },
		"channel":                    func(r *domain.PotentialCommandRequest) { r.Session.ChannelID = 0 },
		"session":                    func(r *domain.PotentialCommandRequest) { r.Session.SessionID = "" },
		"generation":                 func(r *domain.PotentialCommandRequest) { r.Session.Generation = 0 },
		"account snapshot":           func(r *domain.PotentialCommandRequest) { r.Session.Character.AccountID++ },
		"world snapshot":             func(r *domain.PotentialCommandRequest) { r.Session.Character.WorldID++ },
		"magnify has no spirit flag": func(r *domain.PotentialCommandRequest) { r.Action = domain.PotentialMagnify; r.LegendarySpirit = true },
	} {
		t.Run(name, func(t *testing.T) {
			x := r
			mutate(&x)
			s := new(Store) // no DB: invalid inputs must return before BeginTx.
			if _, err := s.ApplyPotential(context.Background(), x); !errors.Is(err, domain.ErrInvalidInput) {
				t.Fatal(err)
			}
			if _, found, err := s.ResolvePotential(context.Background(), x); found || !errors.Is(err, domain.ErrInvalidInput) {
				t.Fatal(found, err)
			}
		})
	}
}

func TestPotentialCommandFreshStateCapacityLifeAndSpirit(t *testing.T) {
	c, r := potentialCommandUnitFixture()
	for name, mutate := range map[string]func(*domain.Character, *domain.PotentialCommandRequest){
		"ambiguous legacy base":        func(c *domain.Character, r *domain.PotentialCommandRequest) { c.VitalsModel = 0 },
		"revision":                     func(c *domain.Character, r *domain.PotentialCommandRequest) { c.Revision++ },
		"dead":                         func(c *domain.Character, r *domain.PotentialCommandRequest) { r.Session.Character.HP = 0 },
		"HP forged":                    func(c *domain.Character, r *domain.PotentialCommandRequest) { r.Session.Character.HP = c.MaxHP + 1 },
		"MP forged":                    func(c *domain.Character, r *domain.PotentialCommandRequest) { r.Session.Character.MP = -1 },
		"source exceeds real capacity": func(c *domain.Character, r *domain.PotentialCommandRequest) { r.SourceSlot = c.Capacity(2) + 1 },
		"target exceeds real capacity": func(c *domain.Character, r *domain.PotentialCommandRequest) { r.TargetSlot = c.Capacity(1) + 1 },
		"no target":                    func(c *domain.Character, r *domain.PotentialCommandRequest) { c.Equipment = nil },
		"no source":                    func(c *domain.Character, r *domain.PotentialCommandRequest) { c.Items = nil },
		"empty material":               func(c *domain.Character, r *domain.PotentialCommandRequest) { c.Items[0].Quantity = 0 },
		"CASH5534000":                  func(c *domain.Character, r *domain.PotentialCommandRequest) { c.Items[0].TemplateID = 5534000 },
		"cash serial":                  func(c *domain.Character, r *domain.PotentialCommandRequest) { c.Items[0].CashID = 1 },
		"no spirit skill": func(c *domain.Character, r *domain.PotentialCommandRequest) {
			r.TargetSlot = 1
			r.LegendarySpirit = true
			c.Equipment[0].Slot = 1
		},
		"no spirit flag": func(c *domain.Character, r *domain.PotentialCommandRequest) {
			r.TargetSlot = 1
			c.Equipment[0].Slot = 1
			c.Skills = []domain.SkillState{{ID: 1003, Level: 1}}
		},
		"spirit on worn": func(c *domain.Character, r *domain.PotentialCommandRequest) { r.LegendarySpirit = true },
	} {
		t.Run(name, func(t *testing.T) {
			current := c
			current.Items = append([]domain.InventoryItem(nil), c.Items...)
			current.Equipment = append([]domain.Equipment(nil), c.Equipment...)
			x := r
			mutate(&current, &x)
			if _, _, err := potentialCommandState(current, x); err == nil {
				t.Fatal("unsafe state accepted")
			}
		})
	}
	bag := c
	bag.Equipment = append([]domain.Equipment(nil), c.Equipment...)
	bag.Equipment[0].Slot = 1
	bag.Skills = []domain.SkillState{{ID: 1003, Level: 1}}
	x := r
	x.TargetSlot, x.LegendarySpirit = 1, true
	if a, b, err := potentialCommandState(bag, x); err != nil || a != 0 || b != 0 {
		t.Fatal(a, b, err)
	}
	bag.Job = 5112
	bag.Skills = []domain.SkillState{{ID: 50001003, Level: 1}}
	if _, _, err := potentialCommandState(bag, x); err != nil {
		t.Fatal("Mihile's learned Legendary Spirit must authorize bag scrolls", err)
	}
	bag.Job = 112
	if _, _, err := potentialCommandState(bag, x); !errors.Is(err, domain.ErrForbidden) {
		t.Fatal("former Mihile cannot use the retained skill in another family", err)
	}
	// Magnify has no Legendary Spirit field in native8E and allows a bag item.
	x.Action, x.LegendarySpirit = domain.PotentialMagnify, false
	bag.Skills = nil
	bag.Items = append([]domain.InventoryItem(nil), c.Items...)
	bag.Items[0].TemplateID = 2460000
	if _, _, err := potentialCommandState(bag, x); err != nil {
		t.Fatal("magnify incorrectly requires spirit", err)
	}
}

func potentialUnitReceipt(t *testing.T) (domain.PotentialCommandRequest, domain.PotentialCommandResult) {
	t.Helper()
	c, r := potentialCommandUnitFixture()
	catalog, err := content.LoadPotentialCatalog(writePotentialCommandTestWZ(t))
	if err != nil {
		t.Fatal(err)
	}
	view, err := content.PotentialOperationTarget(c.Equipment[0], content.Item{ID: c.Equipment[0].ItemID, InvType: 1, ReqLevel: 10})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := content.SealPotential(catalog, content.PotentialSealRequest{Target: view.PolicyTarget, ScrollID: c.Items[0].TemplateID}, content.DefaultPotentialPolicy(), func(n int) (int, error) { return n - 1, nil })
	if err != nil {
		t.Fatal(err)
	}
	result := domain.PotentialCommandResult{Action: r.Action, OperationID: potentialOperationID(r), SourceSlot: r.SourceSlot, TargetSlot: r.TargetSlot, ToolID: c.Items[0].TemplateID,
		ToolInstanceID: c.Items[0].ID, EquipmentID: c.Equipment[0].ID, EquipmentTemplateID: c.Equipment[0].ItemID, Status: 1, EquipmentBefore: c.Equipment[0], BeforeStored: c.Equipment[0].Potential,
		NativeBefore: view.NativeBefore, LogicalPolicyBefore: view.PolicyTarget.Potential, Normalization: view.Normalization}
	result.Inventory.Character = c
	result.Inventory.Character.Equipment = append([]domain.Equipment(nil), c.Equipment...)
	result.Inventory.Character.Equipment[0].Potential = plan.After
	result.Inventory.Character.Items = append([]domain.InventoryItem(nil), c.Items...)
	result.Inventory.Character.Items[0].Quantity--
	afterEquipment := result.Inventory.Character.Equipment[0]
	result.Inventory.Changes = []domain.InventoryChange{{Mode: 1, InvType: 2, Slot: r.SourceSlot, Quantity: 1}, {Mode: 3, InvType: 1, Slot: r.TargetSlot},
		{Mode: 0, InvType: 1, Slot: r.TargetSlot, Equipment: &afterEquipment}}
	result.Inventory.Character.Revision++
	result.CommittedRevision = result.Inventory.Character.Revision
	result.PlanJSON, err = json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	result.PlanSHA256 = potentialPlanDigest(result.PlanJSON)
	if err = validatePotentialReceipt(r, result); err != nil {
		t.Fatal("fixture receipt invalid", err)
	}
	return r, result
}

func TestPotentialReceiptPlanBytesSurviveJSONBAndTamperingFailsClosed(t *testing.T) {
	r, result := potentialUnitReceipt(t)
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var reordered map[string]any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err = d.Decode(&reordered); err != nil {
		t.Fatal(err)
	}
	jsonb, err := json.Marshal(reordered)
	if err != nil {
		t.Fatal(err)
	}
	var restored domain.PotentialCommandResult
	if err = json.Unmarshal(jsonb, &restored); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(restored.PlanJSON, result.PlanJSON) || restored.PlanSHA256 != result.PlanSHA256 || !reflect.DeepEqual(restored, result) {
		t.Fatal("JSONB changed canonical plan/marker presence")
	}
	if err = validatePotentialReceipt(r, restored); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*domain.PotentialCommandResult){
		"published quantity only":                 func(v *domain.PotentialCommandResult) { v.Inventory.Changes[0].Quantity = 99 },
		"published add stats only":                func(v *domain.PotentialCommandResult) { v.Inventory.Changes[2].Equipment.WATK++ },
		"published wrong remove slot":             func(v *domain.PotentialCommandResult) { v.Inventory.Changes[1].Slot-- },
		"published source remove while not empty": func(v *domain.PotentialCommandResult) { v.Inventory.Changes[0].Mode = 3 },
		"extra publication": func(v *domain.PotentialCommandResult) {
			v.Inventory.Changes = append(v.Inventory.Changes, domain.InventoryChange{Mode: 3, InvType: 2, Slot: 9})
		},
		"source instance":     func(v *domain.PotentialCommandResult) { v.ToolInstanceID++ },
		"source row moved":    func(v *domain.PotentialCommandResult) { v.Inventory.Character.Items[0].Slot++ },
		"source row template": func(v *domain.PotentialCommandResult) { v.Inventory.Character.Items[0].TemplateID++ },
		"source row duplicated": func(v *domain.PotentialCommandResult) {
			v.Inventory.Character.Items = append(v.Inventory.Character.Items, v.Inventory.Character.Items[0])
		},
		"equipment row duplicated": func(v *domain.PotentialCommandResult) {
			v.Inventory.Character.Equipment = append(v.Inventory.Character.Equipment, v.Inventory.Character.Equipment[0])
		},
		"unexpected drop":         func(v *domain.PotentialCommandResult) { v.Inventory.Drop = &domain.ItemDrop{ObjectID: 1} },
		"persisted replay marker": func(v *domain.PotentialCommandResult) { v.Replayed = true },
		"plan bytes":              func(v *domain.PotentialCommandResult) { v.PlanJSON = append(append([]byte(nil), v.PlanJSON...), ' ') },
		"plan hash":               func(v *domain.PotentialCommandResult) { v.PlanSHA256 = "bad" },
		"action":                  func(v *domain.PotentialCommandResult) { v.Action = domain.PotentialMagnify },
		"status":                  func(v *domain.PotentialCommandResult) { v.Status = 0 },
		"fee":                     func(v *domain.PotentialCommandResult) { v.MesoFee = 1000 },
		"equipment id":            func(v *domain.PotentialCommandResult) { v.EquipmentID++ },
		"slot":                    func(v *domain.PotentialCommandResult) { v.TargetSlot-- },
		"owner":                   func(v *domain.PotentialCommandResult) { v.Inventory.Character.AccountID++ },
		"world":                   func(v *domain.PotentialCommandResult) { v.Inventory.Character.WorldID++ },
		"revision":                func(v *domain.PotentialCommandResult) { v.CommittedRevision++ },
		"stored present":          func(v *domain.PotentialCommandResult) { v.BeforeStored.Present = true },
		"native flags":            func(v *domain.PotentialCommandResult) { v.NativeBefore.Flags = 0 },
		"normalization":           func(v *domain.PotentialCommandResult) { v.Normalization = "none" },
	} {
		t.Run(name, func(t *testing.T) {
			var bad domain.PotentialCommandResult
			if err := json.Unmarshal(jsonb, &bad); err != nil {
				t.Fatal(err)
			}
			mutate(&bad)
			if err := validatePotentialReceipt(r, bad); !errors.Is(err, domain.ErrForbidden) {
				t.Fatal("ambiguous receipt accepted", err)
			}
		})
	}
	// Exhausted material has no post row and exactly one source-removal frame.
	removed := restored
	removed.Inventory.Character.Items = nil
	removed.Inventory.Changes = append([]domain.InventoryChange(nil), restored.Inventory.Changes...)
	removed.Inventory.Changes[0] = domain.InventoryChange{Mode: 3, InvType: 2, Slot: r.SourceSlot}
	if err := validatePotentialReceipt(r, removed); err != nil {
		t.Fatal("canonical exhausted-source receipt rejected", err)
	}
}
