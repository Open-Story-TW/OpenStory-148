package protocol

const SendMonsterContactDamage uint16 = 0x1c9

// MonsterContact is the v148 CMob-on-CMob body hit emitted by 0x607B96.
type MonsterContact struct {
	Source, Character, Target int32
	Damage                    int32
	X, Y                      int
	Facing                    byte
}

func ReadMonsterContact(r *Reader) (MonsterContact, error) {
	m := MonsterContact{Source: r.I32(), Character: r.I32(), Target: r.I32()}
	mode, attack := r.U8(), r.U8()
	m.Damage = r.I32()
	m.Facing = r.U8()
	m.X, m.Y = int(r.I16()), int(r.I16())
	if r.Err() != nil || r.Remaining() != 0 || mode != 0 || attack != 0xff || m.Facing > 1 ||
		m.Source <= 0 || m.Target <= 0 || m.Source == m.Target || m.Character <= 0 || m.Damage <= 0 {
		return MonsterContact{}, ErrMalformed
	}
	return m, nil
}

// MonsterContactDamage dispatches CMob::OnDamagedByMob (v148 0x607C44).
func MonsterContactDamage(target int32, sourceTemplate int, damage int32, facing byte) []byte {
	w := NewWriter(SendMonsterContactDamage)
	w.I32(target)
	w.U8(0xff) // Body contact has no numbered attack animation.
	w.I32(damage)
	w.I32(int32(sourceTemplate))
	w.U8(facing)
	return w.Packet()
}
