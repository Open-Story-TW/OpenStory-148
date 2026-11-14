package settings

import (
	"testing"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

func TestNormalize(t *testing.T) {
	for _, tc := range []struct{ key, in, want string }{
		{"int_level", "true", "1"}, {"int_level", "off", "0"}, {"max_level", " 500 ", "500"},
		{"exp_rate", "10", "10"}, {"trace_characters", " 3, 12 ,", "3,12"}, {"trace_characters", "", ""},
	} {
		if got, err := Normalize(tc.key, tc.in); err != nil || got != tc.want {
			t.Errorf("Normalize(%q,%q)=%q,%v want %q", tc.key, tc.in, got, err, tc.want)
		}
	}
	for _, tc := range [][2]string{{"int_level", "maybe"}, {"max_level", "255"}, {"exp_rate", "0"}, {"exp_rate", "1001"},
		{"long_damage_cap", "9007199254740992"}, {"trace_characters", "abc"}, {"nope", "1"}} {
		if _, err := Normalize(tc[0], tc[1]); err == nil {
			t.Errorf("Normalize(%q,%q) accepted", tc[0], tc[1])
		}
	}
}

func TestResolveKeepsEnvironmentDefaultForBadRows(t *testing.T) {
	values := Resolve(map[string]string{"exp_rate": "5", "meso_rate": "banana", "gone": "1"})
	if values["exp_rate"] != "5" || values["meso_rate"] != environment["meso_rate"] {
		t.Fatalf("unexpected values: %v", values)
	}
	if _, ok := values["gone"]; ok {
		t.Fatal("unknown key kept")
	}
	if len(values) != len(defs) {
		t.Fatalf("got %d values for %d settings", len(values), len(defs))
	}
}

func TestApplySetsTheLauncherInterfaceMask(t *testing.T) {
	defer apply(Resolve(nil), false)
	values := Resolve(map[string]string{"equip_tooltip": "0", "equip_compare": "1", "merchant_price_units": "0", "chat_unlimited": "0", "chat_repeat_unlimited": "0", "skill_any_order": "0"})
	apply(values, false)
	if got := protocol.ClientUI(); got != protocol.ClientUIEquipCompare {
		t.Fatalf("mask=%#x", got)
	}
	if got := protocol.ClientUIPacket(); string(got) != "\x15\x06\x02\x00\x00\x00" {
		t.Fatalf("packet=%x", got)
	}
	values["equip_tooltip"] = "1"
	apply(values, false)
	if got := protocol.ClientUI(); got != protocol.ClientUIEquipTooltip|protocol.ClientUIEquipCompare {
		t.Fatalf("mask=%#x", got)
	}
}

// Shop price units are on unless the operator turns them off; cash items in
// hired merchants are off unless the operator turns them on.
func TestMerchantSettingsDefaultsAndMask(t *testing.T) {
	defer apply(Resolve(nil), false)
	apply(Resolve(nil), false)
	if got := protocol.ClientUI(); got&protocol.ClientUIMerchantPriceUnits == 0 || got&protocol.ClientUIMerchantCashItems != 0 || domain.MerchantCashItems() {
		t.Fatalf("defaults: mask=%#x cash=%v", got, domain.MerchantCashItems())
	}
	apply(Resolve(map[string]string{"merchant_price_units": "0", "merchant_cash_items": "1"}), false)
	if got := protocol.ClientUI(); got&protocol.ClientUIMerchantPriceUnits != 0 || got&protocol.ClientUIMerchantCashItems == 0 || !domain.MerchantCashItems() {
		t.Fatalf("switched: mask=%#x cash=%v", got, domain.MerchantCashItems())
	}
}

// Cash items in player trades are off unless the operator turns them on, and
// the switch is separate from the hired merchant's.
func TestTradeCashItemsSetting(t *testing.T) {
	defer apply(Resolve(nil), false)
	apply(Resolve(nil), false)
	if protocol.ClientUI()&protocol.ClientUITradeCashItems != 0 || domain.TradeCashItems() {
		t.Fatal("cash items in trades are on by default")
	}
	apply(Resolve(map[string]string{"trade_cash_items": "1"}), false)
	if got := protocol.ClientUI(); got&protocol.ClientUITradeCashItems == 0 || got&protocol.ClientUIMerchantCashItems != 0 || !domain.TradeCashItems() || domain.MerchantCashItems() {
		t.Fatalf("switched: mask=%#x trade=%v merchant=%v", got, domain.TradeCashItems(), domain.MerchantCashItems())
	}
	if !domain.TradeCashAllowed(5072000, 7) || domain.TradeCashAllowed(5072000, 0) || domain.TradeCashAllowed(5000000, 7) || domain.TradeCashAllowed(5030000, 7) {
		t.Fatal("a cash item needs its serial, and pets and shop permits stay out")
	}
}

func TestFeeRateSettings(t *testing.T) {
	defer apply(Resolve(nil), false)
	apply(Resolve(nil), false)
	if domain.MerchantFee(100_000_000) != 3_000_000 || domain.TradeFee(100_000_000) != 6_000_000 {
		t.Fatal("default fee tables are not the Legacy ones")
	}
	normal, err := Normalize("merchant_fee_rates", " 0:1 , 1000000:2.50% ")
	if err != nil || normal != "0:1,1000000:2.5" {
		t.Fatalf("normalize: %q %v", normal, err)
	}
	if _, err = Normalize("trade_fee_rates", "1000000:2,100:1"); err == nil {
		t.Fatal("a table that is not rising was accepted")
	}
	apply(Resolve(map[string]string{"merchant_fee_rates": "0:1,1000000:2.5", "trade_fee_rates": ""}), false)
	if domain.MerchantFee(1000) != 10 || domain.MerchantFee(2_000_000) != 50_000 || domain.TradeFee(100_000_000) != 0 {
		t.Fatalf("custom tables: %d %d %d", domain.MerchantFee(1000), domain.MerchantFee(2_000_000), domain.TradeFee(100_000_000))
	}
	// A saved row that no longer parses keeps the default instead of zero fees.
	apply(Resolve(map[string]string{"merchant_fee_rates": "garbage"}), false)
	if domain.MerchantFee(100_000_000) != 3_000_000 {
		t.Fatal("an invalid saved table replaced the default")
	}
}

func TestCharacterSlotsSetting(t *testing.T) {
	defer apply(Resolve(nil), false)
	if environment["character_slots"] != "60" {
		t.Fatalf("default=%q", environment["character_slots"])
	}
	for _, bad := range []string{"0", "61", "x"} {
		if _, err := Normalize("character_slots", bad); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
	apply(Resolve(map[string]string{"character_slots": "60"}), false)
	if domain.CharacterSlots() != 60 || Active()["character_slots"] != "60" {
		t.Fatalf("slots=%d", domain.CharacterSlots())
	}
	apply(Resolve(map[string]string{"character_slots": "5"}), false)
	if domain.CharacterSlots() != 5 {
		t.Fatalf("slots=%d", domain.CharacterSlots())
	}
}

// The six combat switches are off by default and each sets its own bit,
// bits 5 to 10 of the 0x615 mask.
func TestCombatSwitchBits(t *testing.T) {
	defer apply(Resolve(nil), false)
	apply(Resolve(nil), false)
	const combat = 0x3F << 5
	if got := protocol.ClientUI(); got&combat != 0 {
		t.Fatalf("defaults: mask=%#x", got)
	}
	for i, key := range []string{"ranged_no_melee", "jump_attack_all_jobs", "air_cast", "air_teleport", "morph_skills", "no_hit_stun"} {
		apply(Resolve(map[string]string{key: "1"}), false)
		if got := protocol.ClientUI() & combat; got != 1<<(5+i) {
			t.Errorf("%s: mask=%#x want %#x", key, got, 1<<(5+i))
		}
	}
	all := map[string]string{}
	for _, sw := range combatSwitches {
		all[sw.key] = "on"
	}
	apply(Resolve(all), false)
	if got := protocol.ClientUI() & combat; got != combat {
		t.Fatalf("all on: mask=%#x", got)
	}
}

// The chat flood limits are lifted unless the operator restores them, on the
// server and (bit 11) in the launcher together.
func TestChatUnlimitedSetting(t *testing.T) {
	defer apply(Resolve(nil), false)
	apply(Resolve(nil), false)
	if protocol.ClientUI()&protocol.ClientUIChatUnlimited == 0 || !domain.ChatUnlimited() {
		t.Fatalf("default: mask=%#x server=%v", protocol.ClientUI(), domain.ChatUnlimited())
	}
	if protocol.ClientUIChatUnlimited != 1<<11 {
		t.Fatalf("bit=%#x", protocol.ClientUIChatUnlimited)
	}
	apply(Resolve(map[string]string{"chat_unlimited": "0"}), false)
	if protocol.ClientUI()&protocol.ClientUIChatUnlimited != 0 || domain.ChatUnlimited() {
		t.Fatalf("off: mask=%#x server=%v", protocol.ClientUI(), domain.ChatUnlimited())
	}
	// The repeated line switch is its own bit and on by default.
	if protocol.ClientUIChatRepeat != 1<<12 || protocol.ClientUI()&protocol.ClientUIChatRepeat == 0 {
		t.Fatalf("repeat default: mask=%#x", protocol.ClientUI())
	}
	apply(Resolve(map[string]string{"chat_repeat_unlimited": "0"}), false)
	if got := protocol.ClientUI(); got&protocol.ClientUIChatRepeat != 0 || got&protocol.ClientUIChatUnlimited == 0 || !domain.ChatUnlimited() {
		t.Fatalf("repeat off: mask=%#x server=%v", got, domain.ChatUnlimited())
	}
	// So is the skill order switch.
	apply(Resolve(nil), false)
	if protocol.ClientUISkillAnyOrder != 1<<13 || protocol.ClientUI()&protocol.ClientUISkillAnyOrder == 0 {
		t.Fatalf("skill order default: mask=%#x", protocol.ClientUI())
	}
	apply(Resolve(map[string]string{"skill_any_order": "0"}), false)
	if got := protocol.ClientUI(); got&protocol.ClientUISkillAnyOrder != 0 || got&protocol.ClientUIChatRepeat == 0 {
		t.Fatalf("skill order off: mask=%#x", got)
	}
}
