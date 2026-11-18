package content

const (
	AzwanSweepName = "AzwanSweep"
	AzwanShelter   = 262000000
	AzwanObelisk   = 262010000
	AzwanSweepOne  = 955000100
	AzwanSweepTwo  = 955000200
	AzwanSweepLast = 955000300
)

func azwanSweepEvent() Event {
	return Event{
		Name: AzwanSweepName, LobbyMap: AzwanShelter, ExitMap: AzwanShelter,
		Maps: []int{AzwanSweepOne, AzwanSweepTwo, AzwanSweepLast}, Seconds: 1800,
		PartyMin: 1, PartyMax: 6, Keep: 1, LevelMin: 40, LevelMax: 200,
		RequiredMobs: map[int]map[int]int{
			AzwanSweepOne:  {9302044: 19, 9302045: 17, 9302046: 2},
			AzwanSweepTwo:  {9302044: 19, 9302045: 17, 9302046: 2, 9302047: 1},
			AzwanSweepLast: {9302044: 19, 9302045: 17, 9302046: 2, 9302048: 1},
		},
	}
}
