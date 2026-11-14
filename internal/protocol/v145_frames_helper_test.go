package protocol

import "encoding/hex"

// v148Body decodes an archived v145 attack body (no opcode) into v148 layout.
func v148Body(op uint16, raw string) []byte {
	b, err := hex.DecodeString(raw)
	if err != nil {
		panic(err)
	}
	return UpgradeV145AttackBody(op, b)
}

// v148Wire does the same for a frame that starts with its opcode.
func v148Wire(op uint16, raw string) []byte {
	b, err := hex.DecodeString(raw)
	if err != nil {
		panic(err)
	}
	return append(append([]byte(nil), b[:2]...), UpgradeV145AttackBody(op, b[2:])...)
}

// v148Summon decodes an archived v145 summon attack body into v148 layout.
func v148Summon(raw string) []byte {
	b, err := hex.DecodeString(raw)
	if err != nil {
		panic(err)
	}
	return UpgradeV145SummonAttackBody(b)
}
