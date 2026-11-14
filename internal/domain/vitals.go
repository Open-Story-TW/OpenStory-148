package domain

// Wire maxima are intrinsic GW_CharacterStat values. The client independently
// derives equipped and learned-skill bonuses. Unresolved legacy rows retain
// their prior wire behavior until an explicit base can be provided.
func (c Character) WireMaxHP() int {
	if c.VitalsModel == 1 {
		return c.BaseMaxHP
	}
	return c.MaxHP
}
func (c Character) WireMaxMP() int {
	if fury, ok := DemonFuryMax(c.Job); ok {
		return fury // Java sends getMPByJob for Demon Fury.
	}
	if c.VitalsModel == 1 {
		return c.BaseMaxMP
	}
	return c.MaxMP
}

// DemonFuryMax is the Demon Slayer's fixed Demon Fury maximum (Java
// getMPByJob); it replaces MaxMP.
func DemonFuryMax(job int) (int, bool) {
	switch job {
	case 3001, 3100:
		return 30, true
	case 3110:
		return 50, true
	case 3111:
		return 100, true
	case 3112:
		return 120, true
	}
	return 0, false
}
