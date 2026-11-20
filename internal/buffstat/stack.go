package buffstat

// stackedForms maps an additive triplet stat to the indie stat that carries
// the same client effect. The client keeps a list of entries (source, value,
// duration) per indie stat and sums them into the total (sub_6BCC80 PAD,
// sub_6BCFAE MAD, and the ACC/EVA/speed/jump totals), so rows of different
// sources stack natively. WDEF/MDEF have no audited indie counterpart yet.
var stackedForms = map[Stat]Stat{
	"WATK":  "ANGEL_ATK",
	"MATK":  "ANGEL_MATK",
	"ACC":   "ANGEL_ACC",
	"AVOID": "ANGEL_AVOID",
	"SPEED": "ANGEL_SPEED",
	"JUMP":  "ANGEL_JUMP",
}

// StackedForm returns the indie stat a stacking buff uses for name, or name
// itself when it has none.
func StackedForm(name Stat) Stat {
	if indie, ok := stackedForms[name]; ok {
		return indie
	}
	return name
}

// Stackable reports whether rows of different sources may hold name at the
// same time: only indie stats, whose client list keeps each source apart.
func Stackable(name Stat) bool {
	s, ok := Lookup(name)
	return ok && s.IndieOrder > 0
}

// EffectSlot identifies distinct logical effects that share a single native
// slot. Spirit Link and Spirit Surge cannot coexist in bit169.
func EffectSlot(name string) string {
	if name == "SPIRIT_LINK" {
		return "SPIRIT_SURGE"
	}
	return name
}

// SpiritLinkSkill gives the learned source for a bird's active bit169 row.
// Current persisted rows use the canonical SPIRIT_SURGE name. Older rows may
// still carry SPIRIT_LINK from before the shared native slot was identified.
func SpiritLinkSkill(summon int, stat string) int {
	if EffectSlot(stat) != "SPIRIT_SURGE" {
		return 0
	}
	switch summon {
	case 3111005:
		return 3120006
	case 3211005:
		return 3220005
	}
	return 0
}
