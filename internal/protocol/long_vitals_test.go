package protocol

import (
	"testing"

	"maple-go-server/internal/domain"
)

func TestLongVitalsIsAnnouncedAndPartyHPAcceptsIt(t *testing.T) {
	defer domain.SetLongVitalsForTest(true)()
	if ClientFeatureLongVitals != 1<<7 || ClientConfigFeatures()&ClientFeatureLongVitals == 0 {
		t.Fatal("the launcher must be told max HP and MP may pass 99,999 (bit 7)")
	}
	if PartyMemberHP(1, 5_000_000, domain.VitalCeiling) == nil {
		t.Fatal("party HP above 99,999 refused")
	}
	if PartyMemberHP(1, 1, domain.VitalCeiling+1) != nil {
		t.Fatal("party HP above the ceiling accepted")
	}
	domain.SetLongVitalsForTest(false)
	if ClientConfigFeatures()&ClientFeatureLongVitals != 0 {
		t.Fatal("bit 7 announced with long vitals off")
	}
}
