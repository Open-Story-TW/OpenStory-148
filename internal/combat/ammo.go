package combat

// AmmoOf classifies a USE item as a projectile.
func AmmoOf(itemID int) Ammo {
	switch {
	case itemID/1000 == 2060:
		return AmmoArrow
	case itemID/1000 == 2061:
		return AmmoCrossbowArrow
	case itemID/10000 == 207:
		return AmmoStar
	case itemID/10000 == 233:
		return AmmoBullet
	}
	return AmmoAuto
}

// AmmoItemMatches reports whether the item is any projectile.
func AmmoItemMatches(itemID int) bool { return AmmoOf(itemID) != AmmoAuto }
