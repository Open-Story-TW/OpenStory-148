package world

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"testing"

	"maple-go-server/internal/protocol"
)

func TestCharacterTraceIsExactOptIn(t *testing.T) {
	t.Setenv("MAPLEGO_TRACE_CHARACTERS", "4910, invalid,4917,0,-1")
	for _, id := range []int64{4910, 4917} {
		if !traceCharacterEnabled(id) {
			t.Fatal("selected QA identity omitted", id)
		}
	}
	for _, id := range []int64{0, -1, 4911, 14910} {
		if traceCharacterEnabled(id) {
			t.Fatal("unselected or unauthenticated identity traced", id)
		}
	}
	t.Setenv("MAPLEGO_TRACE_CHARACTERS", "")
	if traceCharacterEnabled(4910) {
		t.Fatal("trace enabled by default")
	}
}

func TestCharacterPacketTracePreservesFullTailAndLegacyPrefix(t *testing.T) {
	packet := append([]byte{0xea, 0}, bytes.Repeat([]byte{0x45, 0x81}, 128)...)
	before := bytes.Clone(packet)
	wantHash := sha256.Sum256(packet)
	for _, full := range []bool{false, true} {
		var output bytes.Buffer
		logger := slog.New(slog.NewJSONHandler(&output, nil))
		recordCharacterPacket(logger, "trace_out", 4917, full, packet)
		var row struct {
			Message string `json:"msg"`
			CID     int64  `json:"character"`
			Opcode  string `json:"opcode"`
			Length  int    `json:"len"`
			Prefix  string `json:"hex"`
			Hash    string `json:"wire_sha256"`
			Full    bool   `json:"full_packet"`
			Wire    string `json:"wire_hex"`
		}
		if err := json.Unmarshal(output.Bytes(), &row); err != nil {
			t.Fatal(err)
		}
		if row.Message != "trace_out" || row.CID != 4917 || row.Opcode != "0xea" || row.Length != len(packet) || row.Prefix != hex.EncodeToString(packet[:96]) || row.Hash != hex.EncodeToString(wantHash[:]) || row.Full != full {
			t.Fatal("packet trace lost identity, original bytes or digest", row)
		}
		if full && row.Wire != hex.EncodeToString(packet) || !full && row.Wire != "" || !bytes.Equal(packet, before) {
			t.Fatal("capture truncated the full tail, leaked it by default or changed the packet")
		}
	}
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	for _, malformed := range [][]byte{nil, {0xea}, make([]byte, protocol.MaxPacketSize+1)} {
		recordCharacterPacket(logger, "trace_in", 4917, true, malformed)
	}
	if output.Len() != 0 {
		t.Fatal("malformed or oversized packet was logged")
	}
}
