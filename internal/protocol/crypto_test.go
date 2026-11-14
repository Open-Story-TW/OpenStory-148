package protocol

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
)

// These vectors were emitted by a separately compiled copy of Legacy's Java
// MapleAESOFB.java with its default AES key; no Java service/database was used.
func TestJavaAESVectors(t *testing.T) {
	data, err := os.ReadFile("testdata/crypto_vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors []struct {
		Length int    `json:"length"`
		Header string `json:"header"`
		SHA    string `json:"cipher_sha256"`
		IV     string `json:"next_iv"`
	}
	if err := json.Unmarshal(data, &vectors); err != nil {
		t.Fatal(err)
	}
	for _, vector := range vectors {
		c := newAES([4]byte{0x4d, 0x23, 0xc7, 0x2b}, 145)
		header := c.header(vector.Length)
		if got := hex.EncodeToString(header[:]); got != vector.Header {
			t.Fatalf("size%d header=%s want%s", vector.Length, got, vector.Header)
		}
		if !c.validHeader(header) || packetLength(header) != vector.Length {
			t.Fatal("header validation/length mismatch")
		}
		packet := make([]byte, vector.Length)
		for i := range packet {
			packet[i] = byte(i*31 + 7)
		}
		c.crypt(packet)
		sum := sha256.Sum256(packet)
		if got := hex.EncodeToString(sum[:]); got != vector.SHA {
			t.Fatalf("size%d cipher=%s want%s", vector.Length, got, vector.SHA)
		}
		if got := hex.EncodeToString(c.iv[:]); got != vector.IV {
			t.Fatalf("shuffle=%s want%s", got, vector.IV)
		}
	}
}
