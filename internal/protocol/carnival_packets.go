package protocol

import "errors"

const RecvCarnivalAction uint16 = 0x193

const (
	carnivalStart          = 0x1FC
	carnivalCP             = 0x1FD
	carnivalScore          = 0x1FE
	carnivalCooldowns      = 0x1FF
	carnivalActionAccepted = 0x200
	carnivalActionError    = 0x201
	carnivalDeath          = 0x202
	carnivalRanking        = 0x205
)

// TimedQuestInvitation follows mode 13 of the native 0x58 message. The
// expiry is relative to the client's current milliseconds.
func TimedQuestInvitation(questID int, remainingMilliseconds int) []byte {
	w := NewWriter(0x58)
	w.U8(13)
	w.U16(uint16(questID))
	w.I32(int32(remainingMilliseconds))
	return w.Packet()
}

// ReadCarnivalAction follows the one-byte request built at 0x7F8401.
func ReadCarnivalAction(body []byte) (byte, error) {
	if len(body) != 1 || body[0] > 3 {
		return 0, errors.New("invalid Monster Carnival action")
	}
	return body[0], nil
}

// CarnivalStart follows the v148 Type-10 and Type-15 handler at 0x533AAE.
// The score fields are relative to the recipient's team. The remaining
// bytes are one entry for each summon definition loaded by the client.
func CarnivalStart(team byte, available, earned, teamTotal, opponentTotal int, summons int) []byte {
	w := NewWriter(carnivalStart)
	w.U8(team)
	w.I32(int32(available))
	w.I32(int32(earned))
	w.I32(int32(teamTotal))
	w.I32(int32(opponentTotal))
	for range summons {
		w.U8(0)
	}
	return w.Packet()
}

func CarnivalCP(available, earned int) []byte {
	w := NewWriter(carnivalCP)
	w.I32(int32(available))
	w.I32(int32(earned))
	return w.Packet()
}

// CarnivalScore puts the red score first, as 0x533AAE does when normalizing
// the recipient-relative start packet for the same native scoreboard.
func CarnivalScore(red, blue int) []byte {
	w := NewWriter(carnivalScore)
	w.I32(int32(red))
	w.I32(int32(blue))
	return w.Packet()
}

func CarnivalCooldowns(remaining [4]int) []byte {
	w := NewWriter(carnivalCooldowns)
	for _, ms := range remaining {
		w.I32(int32(ms))
	}
	return w.Packet()
}

// The accepted-action decoder at 0x5352CD uses the action and the selected
// WZ definition to display who summoned a monster or used an ability.
func CarnivalActionAccepted(action, selection byte, name string) []byte {
	w := NewWriter(carnivalActionAccepted)
	w.U8(action)
	w.U8(selection)
	w.String(name)
	return w.Packet()
}

func CarnivalActionError(reason byte) []byte {
	w := NewWriter(carnivalActionError)
	w.U8(reason)
	return w.Packet()
}

func CarnivalDeath(team byte, name string, lostPoints int) []byte {
	w := NewWriter(carnivalDeath)
	w.U8(team)
	w.String(name)
	w.U8(uint8(lostPoints))
	return w.Packet()
}

type CarnivalRank struct {
	CharacterID int64
	Name        string
	Points      int
	Team        byte
}

func CarnivalRanking(ranks []CarnivalRank) []byte {
	w := NewWriter(carnivalRanking)
	w.U16(uint16(len(ranks)))
	for _, rank := range ranks {
		w.I32(int32(rank.CharacterID))
		w.String(rank.Name)
		w.I32(int32(rank.Points))
		w.U8(rank.Team)
	}
	return w.Packet()
}
