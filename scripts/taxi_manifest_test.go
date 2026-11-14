package scripts

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestTaxiManifestPinsEmbeddedSequentialSource(t *testing.T) {
	m, err := TaxiMigrations()
	if err != nil || len(m) != 1 {
		t.Fatal(m, err)
	}
	q := m[0]
	if q.NPCID != 1002007 || q.SourceMapID != 104000000 || q.Legacy != "scripts/npc/1002007.js" || q.SourceSHA256 != "01e30d95f6912da9ae7a32bd17ed4f1a35f74c9105b28694b7c9b10e1f8d4234" || q.NativeMCP != "pending" {
		t.Fatal(q)
	}
	src, err := Source(q.Lua)
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(src)
	if hex.EncodeToString(h[:]) != q.LuaSHA256 {
		t.Fatal("Lua/source manifest drift")
	}
	all, err := Manifest()
	if err != nil || all[q.Lua] != q.LuaSHA256 {
		t.Fatal("not embedded/registered", err)
	}
}
