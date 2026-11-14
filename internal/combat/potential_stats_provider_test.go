package combat

import (
	"errors"
	"testing"
	"time"

	"maple-go-server/internal/domain"
)

type checkedPotentialProvider struct {
	unchecked *bool
	err       error
}

func (p checkedPotentialProvider) PlayerStats(domain.Character, time.Time) PlayerStats {
	*p.unchecked = true
	return PlayerStats{STR: 4}
}
func (p checkedPotentialProvider) PlayerStatsChecked(domain.Character, time.Time) (PlayerStats, error) {
	return PlayerStats{}, p.err
}

func TestCheckedStatsProviderNeverFallsBackAfterSourceError(t *testing.T) {
	statsMu.RLock()
	before := statsSource
	statsMu.RUnlock()
	t.Cleanup(func() { SetStatsProvider(before) })
	called := false
	sourceErr := errors.New("unknown revealed option")
	SetStatsProvider(checkedPotentialProvider{unchecked: &called, err: sourceErr})
	if _, err := AttackStats(domain.Character{}, time.Now()); !errors.Is(err, sourceErr) || called {
		t.Fatal("checked failure bypassed", err, called)
	}
	SetStatsProvider(StatsFunc(func(domain.Character, time.Time) PlayerStats { return PlayerStats{STR: 7} }))
	if got, err := AttackStats(domain.Character{}, time.Now()); err != nil || got.STR != 7 {
		t.Fatal("legacy provider incompatible", got, err)
	}
}
