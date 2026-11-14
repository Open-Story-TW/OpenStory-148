package content

// NativeContactDamagePotential describes a line consumed by the original
// client's ordinary contact-damage calculation. It is not a server-side roll
// or a second damage reduction. The server receives that calculation's final
// damage in the native contact packet and applies the existing HP/MP rules.
//
// v145 86A70E reads prop and ignoreDAM; 86EBBE groups amounts by prop, tests
// Random()%101 <= prop once for each distinct prop, then subtracts the group's
// amount. Flat groups precede percentage groups and the final minimum is 1.
// Preserve individual sources here: adding prop values would change that rule.
// This recognizes the native policy, not whether a particular hit will proc.
// The original client retains its field-36/subtype and secondary-state gates;
// a descriptor does not assert that those gates currently allow the effect.
type NativeContactDamagePotential struct {
	Source    PotentialEffectLine `json:"source"`
	Prop      int                 `json:"prop"`
	IgnoreDAM int                 `json:"ignore_damage"`
}

func nativeContactDamagePotential(line PotentialEffectLine) (NativeContactDamagePotential, bool) {
	// 86EBBE scans these physical worn slots. Do not advertise this consumer
	// for weapon/accessory rows which that native loop does not visit.
	switch line.Slot {
	case -1, -5, -6, -7, -8, -9, -10, -22:
	default:
		return NativeContactDamagePotential{}, false
	}
	v := line.Attributes.Values
	prop, hasProp := v["prop"]
	amount, hasAmount := v["ignoreDAM"]
	if len(line.Attributes.Text) != 0 || len(v) != 2 || !hasProp || !hasAmount ||
		prop < 1 || prop > 100 || amount < 1 || int64(amount) > 1<<31-1 {
		return NativeContactDamagePotential{}, false
	}
	return NativeContactDamagePotential{Source: line, Prop: prop, IgnoreDAM: amount}, true
}
