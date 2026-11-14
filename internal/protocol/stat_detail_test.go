package protocol

import (
	"bytes"
	"encoding/hex"
	"testing"
)

func TestStatDetailPacketGoldenBytes(t *testing.T) {
	got := StatDetailPacket(StatDetail{
		ExpRate: 0x01020304, DropRate: 0x05060708, MesoRate: 0x090A0B0C, PointRate: 0x0D0E0F10,
		DamageCap:  0x1112131415161718, // above 2^32
		CritDamage: 0x191A1B1C, TotalDamage: 0x1D1E1F20, BossDamage: 0x21222324, IgnoreDefense: 0x25262728,
		ReflectDamage: 0x292A2B2C, ReflectChance: 0x2D2E2F30, HPSteal: 0x31323334, MPSteal: 0x35363738,
		HPStealChance: 0x393A3B3C, MPStealChance: 0x3D3E3F40, StatusResist: 0x41424344, ElementResist: 0x45464748,
		DamageReduce: 0x494A4B4C, AvoidChance: -2,
	})
	want, err := hex.DecodeString("1406" + "01" +
		"04030201" + "08070605" + "0c0b0a09" + "100f0e0d" +
		"1817161514131211" +
		"1c1b1a19" + "201f1e1d" + "24232221" + "28272625" +
		"2c2b2a29" + "302f2e2d" + "34333231" + "38373635" +
		"3c3b3a39" + "403f3e3d" + "44434241" + "48474645" +
		"4c4b4a49" + "feffffff")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2+81 {
		t.Fatalf("length %d, want %d", len(got), 2+81)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("packet\n got %x\nwant %x", got, want)
	}
}

func TestStatDetailPacketLongDamageCap(t *testing.T) {
	got := StatDetailPacket(StatDetail{ExpRate: 100, DropRate: 100, MesoRate: 100, PointRate: 100, DamageCap: 99999999999})
	// 99999999999 = 0x174876E7FF
	if want := []byte{0xFF, 0xE7, 0x76, 0x48, 0x17, 0, 0, 0}; !bytes.Equal(got[19:27], want) {
		t.Fatalf("damage cap bytes %x, want %x", got[19:27], want)
	}
	if !bytes.Equal(got[:7], []byte{0x14, 0x06, 1, 100, 0, 0, 0}) {
		t.Fatalf("header %x", got[:7])
	}
}
