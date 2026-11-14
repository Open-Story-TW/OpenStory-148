package protocol

import (
	"sync/atomic"

	"maple-go-server/internal/domain"
)

// The launcher takes its wire-contract switches from the server instead of a
// local file, so the two cannot disagree. The login server sends
// SendClientConfig before its first native packet; the launcher installs the
// matching patches before it decodes anything else. SendClientPreference
// changes one of the player's own display preferences, which the launcher
// applies and keeps on the player's machine.
// SendClientUI switches the launcher's interface additions. They change no
// packet layout, so the packet may be repeated with a new mask at any time.
// 0x614 belongs to the stat detail packet.
// Wire format: Open-Maple docs/SERVER_CONFIG.md.
const (
	SendClientConfig     uint16 = 0x612
	SendClientPreference uint16 = 0x613
	SendClientUI         uint16 = 0x615
	// SendClientFees carries the fee tables the launcher shows and applies
	// in its own previews. 0x616 belongs to the panel packet.
	SendClientFees uint16 = 0x617

	clientConfigVersion = 1
)

// Client config feature bits.
const (
	ClientFeatureIntLevel uint32 = 1 << iota
	ClientFeatureLongStats
	ClientFeatureLongExperience
	ClientFeatureLongDamage
	ClientFeatureSnipeBossFormula
	ClientFeatureLongMoney
	// ClientFeatureLongMerchant: every meso field of the hired merchant and
	// store bank packets is 8 bytes (docs/v148/HIRED_MERCHANT.md). The server
	// sets it together with ClientFeatureLongMoney.
	ClientFeatureLongMerchant
	// ClientFeatureLongVitals: max HP and MP may reach domain.VitalCeiling
	// instead of 99,999. No packet layout changes; the launcher lifts the
	// client's own clamp (Open-Maple docs/LONG_VITALS.md).
	ClientFeatureLongVitals
	// ClientFeatureLargeGuild: guild member counts are two bytes (the guild
	// record's member count, the capacity change and the MULTICHAT recipient
	// count), so a guild may have more than 255 members.
	ClientFeatureLargeGuild
	// ClientFeatureLargeBuddy: the friend count, the friend capacity change
	// and the character data's friend capacity are two bytes, so a friend
	// list may hold more than 255 entries.
	ClientFeatureLargeBuddy
)

// Client interface bits.
const (
	// ClientUIEquipTooltip lays the equipment tooltip out in titled sections.
	ClientUIEquipTooltip uint32 = 1 << iota
	// ClientUIEquipCompare shows how an inventory equip differs from the worn one.
	ClientUIEquipCompare
	// ClientUIMerchantPriceUnits writes shop prices with 兆/億/萬 units.
	ClientUIMerchantPriceUnits
	// ClientUIMerchantCashItems lets cash items be dragged into a hired merchant.
	ClientUIMerchantCashItems
	// ClientUITradeCashItems lets cash items be dragged into a player trade.
	ClientUITradeCashItems
	// Bits 5-10 lift client-side combat and movement limits. They change no
	// packet layout; docs/v148/COMBAT_SWITCHES.md lists the native checks.
	// ClientUIRangedNoMelee: a bow, crossbow, claw or gun always shoots; the
	// close-range swing at a nearby monster is skipped.
	ClientUIRangedNoMelee
	// ClientUIJumpAttackAllJobs: every job may use attack skills in the air,
	// as thieves and Battle Mages may.
	ClientUIJumpAttackAllJobs
	// ClientUIAirCast: summons, mounts and transformations (Mechanic, Wild
	// Hunter), and ground-only attack and movement skills work in the air.
	ClientUIAirCast
	// ClientUIAirTeleport: Teleport (mages, Evan, Battle Mage) works in the air.
	ClientUIAirTeleport
	// ClientUIMorphSkills: skills and attacks work while transformed, without
	// cancelling the transformation first.
	ClientUIMorphSkills
	// ClientUINoHitStun: a hit never knocks the character back or takes
	// control away, as with Power Stance.
	ClientUINoHitStun
)

// Chat flood bits of the client's CChatHelper check. Bits 5 to 10 are taken
// by other interface switches.
const (
	// ClientUIChatUnlimited lifts the limit of 4 lines in 2 seconds and the
	// 2.8 second lockout after a refused line.
	ClientUIChatUnlimited uint32 = 1 << 11
	// ClientUIChatRepeat lets the same line be sent more than three times in
	// a row.
	ClientUIChatRepeat uint32 = 1 << 12
	// ClientUISkillAnyOrder lifts the skill window's job order rule: a skill
	// point goes to any advancement's skills, whatever was spent on the
	// earlier ones.
	ClientUISkillAnyOrder uint32 = 1 << 13
)

// Client preference keys.
const (
	ClientPrefReset byte = iota
	ClientPrefWindowed
	ClientPrefResolution
	ClientPrefMaxFPS
	ClientPrefRenderLayer
	ClientPrefBackgroundInput
)

// ClientConfigFeatures is the feature mask of the contracts this process runs.
func ClientConfigFeatures() uint32 {
	// The server always scores Snipe with the boss formula.
	features := ClientFeatureSnipeBossFormula
	if domain.IntLevelEnabled() {
		features |= ClientFeatureIntLevel
	}
	if domain.LongStatsEnabled() {
		features |= ClientFeatureLongStats
	}
	if domain.LongExperienceEnabled() {
		features |= ClientFeatureLongExperience
	}
	if LongDamageCap() > 0 {
		features |= ClientFeatureLongDamage
	}
	if domain.LongMoneyEnabled() {
		features |= ClientFeatureLongMoney | ClientFeatureLongMerchant
	}
	if domain.LongVitalsEnabled() {
		features |= ClientFeatureLongVitals
	}
	if domain.LargeGuildEnabled() {
		features |= ClientFeatureLargeGuild
	}
	if domain.LargeBuddyEnabled() {
		features |= ClientFeatureLargeBuddy
	}
	return features
}

// ClientConfigPacket announces the active wire contracts to the launcher.
func ClientConfigPacket() []byte {
	w := NewWriter(SendClientConfig)
	w.U8(clientConfigVersion)
	w.U32(ClientConfigFeatures())
	w.U32(uint32(domain.MaxCharacterLevel()))
	limit := LongDamageCap()
	w.U32(uint32(limit))
	w.U32(uint32(limit >> 32))
	return w.Packet()
}

// ClientPreferencePacket sets one display preference on the launcher.
func ClientPreferencePacket(key byte, value int32) []byte {
	w := NewWriter(SendClientPreference)
	w.U8(key)
	w.U32(uint32(value))
	return w.Packet()
}

var clientUI atomic.Uint32

// ClientUI is the mask of interface additions the launcher should show.
func ClientUI() uint32 { return clientUI.Load() }

// SetClientUI replaces the mask; connected players get it on their next map.
func SetClientUI(mask uint32) { clientUI.Store(mask) }

// ClientUIPacket tells the launcher which interface additions are on.
func ClientUIPacket() []byte {
	w := NewWriter(SendClientUI)
	w.U32(ClientUI())
	return w.Packet()
}

const clientFeesVersion = 1

func addFeeTiers(w *Writer, tiers []domain.FeeTier) {
	w.U8(byte(len(tiers)))
	for _, tier := range tiers {
		w.I64(tier.From)
		w.U32(uint32(tier.Rate))
	}
}

// ClientFeesPacket tells the launcher the fee tables in effect, so the shop
// dialog's fee table and the owner's after-fee totals show what the server
// will charge. The v148 trade window shows no fee, so the trade table is
// informational on the client. Layout: version u8, then two
// tables (hired merchant, player trade), each count u8 followed by count
// times {from i64, rate u32}. rate is in parts per 100,000; a fee is rounded
// half up; the last tier whose from is not above the amount applies.
func ClientFeesPacket() []byte {
	w := NewWriter(SendClientFees)
	w.U8(clientFeesVersion)
	addFeeTiers(w, domain.MerchantFees())
	addFeeTiers(w, domain.TradeFees())
	return w.Packet()
}

// SendClientParcel tells the launcher how long a normal parcel takes, so the
// parcel window's 「宅配配送需要消耗12小時」, which is part of its background
// image, can be written over with the server's time. Layout: version u8 (1),
// delay in minutes u32 (0 = arrives at once). Sent on every scene entry and
// again when the parcel window opens, so a changed setting shows without a
// restart. A launcher without the packet ignores it.
const (
	SendClientParcel    uint16 = 0x618
	clientParcelVersion        = 1
)

func ClientParcelPacket() []byte {
	w := NewWriter(SendClientParcel)
	w.U8(clientParcelVersion)
	w.U32(uint32(domain.ParcelDelay().Minutes()))
	return w.Packet()
}
