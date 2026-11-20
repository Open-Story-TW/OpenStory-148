package domain

type InnerAbility struct {
	ID    int `json:"skill_id"`
	Level int `json:"level"`
	Grade int `json:"grade"`
}

type InnerAbilities [3]InnerAbility

func (line InnerAbility) Valid() bool {
	return line.ID/10000 == 7000 && line.Level >= 1 && line.Level <= 40 && line.Grade >= 0 && line.Grade <= 3
}

// InnerAbilitySlots matches the original v148 gate at 0x56AD3D.
func (c Character) InnerAbilitySlots() int {
	switch {
	case c.HonorLevel >= 70:
		return 3
	case c.HonorLevel >= 30:
		return 2
	case c.HonorLevel >= 2:
		return 1
	default:
		return 0
	}
}
