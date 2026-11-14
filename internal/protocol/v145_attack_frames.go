package protocol

// The attack tests keep native frames captured from the v145 client. These
// helpers rewrite such a frame body (without the opcode) into the v148
// layout that ReadAttack and ReadSummonAttack now read, so the archived
// evidence keeps covering the same values:
//   - melee/body: the five bytes after the display become i32 + action
//     (v145 was 2 bytes, u8, action, u8), and melee gains one i32 after tick;
//   - magic: the keydown charge moves after the one/two header bytes;
//   - every target gains a zero i32 after its first three metadata bytes;
//   - summon targets gain a zero mob template ID after the object ID.
// Meso Explosion frames are returned unchanged.

// UpgradeV145AttackBody converts a v145 0x4B/0x4C/0x4D/0x4E body; op is the
// v148 opcode the body will be read with.
func UpgradeV145AttackBody(op uint16, body []byte) []byte {
	if len(body) < 6 {
		return append([]byte(nil), body...)
	}
	in := body
	out := make([]byte, 0, len(body)+64)
	take := func(n int) []byte {
		if n > len(in) {
			n = len(in)
		}
		b := in[:n]
		in = in[n:]
		return b
	}
	copyN := func(n int) { out = append(out, take(n)...) }
	tbyte := body[1]
	skill := int(int32(uint32(body[2]) | uint32(body[3])<<8 | uint32(body[4])<<16 | uint32(body[5])<<24))
	q := QuirkFor(skill)
	copyN(6)
	switch op {
	case RecvMeleeAttack, RecvBodyAttack:
		head := q.PreSkip + 2 + 2
		if q.EnergyHeader {
			head = q.PreSkip + 1 + 2
		}
		if q.Charge {
			head += 4
		}
		head += 4*q.ChargeExtra + 1 + 1 + 2
		copyN(head)
		b := take(5)
		if len(b) == 5 {
			out = append(out, b[0], b[1], b[2], b[4], b[3])
		} else {
			out = append(out, b...)
		}
		copyN(4*q.PreSpeed + 1 + 4 + 4)
		if op == RecvMeleeAttack {
			out = append(out, 0, 0, 0, 0)
		}
		if q.MesoExplode {
			return append(out, in...)
		}
		copyN(q.PreTargets)
	case RecvShootAttack:
		head := 6 + 1 + 2 + 5 + 4*q.PreSpeed + 1 + 4 + 4 + 2 + 2 + 1
		if q.KeyDown {
			head += 4
		}
		copyN(head)
	case RecvMagicAttack:
		copyN(4)
		var charge []byte
		if q.Charge {
			charge = append([]byte(nil), take(4)...)
		}
		if q.WideHeader {
			copyN(2)
		} else {
			copyN(1)
		}
		out = append(out, charge...)
		copyN(1 + 2 + 5 + 1 + 4 + 4)
	default:
		return append(out, in...)
	}
	lines := int(tbyte & 15)
	for i := 0; i < int(tbyte>>4) && len(in) >= 4+14; i++ {
		copyN(4 + 3)
		out = append(out, 0, 0, 0, 0)
		copyN(11 + 4*lines + 8)
	}
	return append(out, in...)
}

// UpgradeV145SummonAttackBody converts a v145 summon attack body: each target
// gains a zero mob template ID after its object ID.
func UpgradeV145SummonAttackBody(body []byte) []byte {
	const head = 4 + 4 + 1 + 1 + 4 + 8
	if len(body) < head {
		return append([]byte(nil), body...)
	}
	out := append([]byte(nil), body[:head]...)
	in := body[head:]
	for i := 0; i < int(body[9]) && len(in) >= 4+18+4+4; i++ {
		out = append(out, in[:4]...)
		out = append(out, 0, 0, 0, 0)
		out = append(out, in[4:4+18+4+4]...)
		in = in[4+18+4+4:]
	}
	return append(out, in...)
}
