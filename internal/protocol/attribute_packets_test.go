package protocol

import (
	"encoding/binary"
	"testing"
)

func TestNativeAttributeRequestUsesEightByteMask(t *testing.T) {
	input := []byte{1, 2, 3, 4, 0x40, 0, 0, 0, 0, 0, 0, 0}
	got, err := ReadAttributeAllocation(NewReader(input))
	if err != nil || got.ClientTick != 0x04030201 || got.Stat != 0x40 {
		t.Fatalf("native AP: %+v %v", got, err)
	}
	for _, input := range [][]byte{input[:8], append(append([]byte(nil), input...), 0)} {
		if _, err := ReadAttributeAllocation(NewReader(input)); err == nil {
			t.Fatal("accepted truncated or trailing AP request")
		}
	}
}

func TestNativeAutoAttributeRequestUsesCountAndFullMasks(t *testing.T) {
	input := make([]byte, 32)
	binary.LittleEndian.PutUint32(input, 123)
	binary.LittleEndian.PutUint32(input[4:], 2)
	binary.LittleEndian.PutUint64(input[8:], 0x40)
	binary.LittleEndian.PutUint32(input[16:], 3)
	binary.LittleEndian.PutUint64(input[20:], 0x80)
	binary.LittleEndian.PutUint32(input[28:], 2)
	q, err := ReadAttributeAutoAllocation(NewReader(input))
	if err != nil || q.ClientTick != 123 || q.Stat != 0 || len(q.Allocations) != 2 || q.Allocations[0].Stat != 0x40 || q.Allocations[0].Amount != 3 || q.Allocations[1].Stat != 0x80 || q.Allocations[1].Amount != 2 {
		t.Fatalf("native automatic AP: %+v %v", q, err)
	}
	for _, n := range []uint32{0, 1, 3, 5, 0xffffffff} {
		bad := append([]byte(nil), input...)
		binary.LittleEndian.PutUint32(bad[4:], n)
		if _, err := ReadAttributeAutoAllocation(NewReader(bad)); err == nil {
			t.Fatalf("accepted mismatched count %d", n)
		}
	}
	for _, bad := range [][]byte{input[:7], input[:31], append(append([]byte(nil), input...), 0)} {
		if _, err := ReadAttributeAutoAllocation(NewReader(bad)); err == nil {
			t.Fatal("accepted truncated or trailing batch")
		}
	}
	// Keep signed amounts and high mask bits for the domain validator; never
	// truncate a malicious 64-bit stat to a valid 32-bit target.
	binary.LittleEndian.PutUint64(input[8:], 0x100000040)
	binary.LittleEndian.PutUint32(input[16:], 0xffffffff)
	q, err = ReadAttributeAutoAllocation(NewReader(input))
	if err != nil || q.Allocations[0].Stat != 0x100000040 || q.Allocations[0].Amount != -1 {
		t.Fatalf("lost native field width: %+v %v", q, err)
	}
}
