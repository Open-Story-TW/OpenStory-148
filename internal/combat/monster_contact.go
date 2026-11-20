package combat

func IsMindControlSkill(skill int) bool {
	return skill == 5221009 || skill == 5921009
}

// MonsterContactBounds is v148's mob-to-mob PAD roll (0x6F327B), followed by
// the target's PDR percentage. It truncates once after the roll and again
// after defense, with a native minimum of 1.
func MonsterContactBounds(pad, pdr int) (int, int) {
	defense := (100.0 - float64(max(0, min(pdr, 100)))) / 100.0
	return max(1, int(float64(int(0.85*float64(pad)))*defense)), max(1, int(float64(pad)*defense))
}
