package protocol

import (
	"bytes"
	"testing"
)

func TestPortalDenialUsesNativeNonmodalTypeFiveChat(t *testing.T) {
	if got := PortalTrainingMessage("No"); !bytes.Equal(got, []byte{0x58, 0x00, 5, 2, 0, 'N', 'o'}) {
		t.Fatalf("native0x94444F type5%x", got)
	}
}
