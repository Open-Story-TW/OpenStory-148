package domain

import (
	"context"
	"errors"
	"strings"

	"golang.org/x/text/encoding/traditionalchinese"
)

// Legacy creates new characters with 232 buddy slots. The operator's
// buddy_capacity raises every list (EffectiveBuddyCapacity); this limit is
// independent of account character slots.
const DefaultBuddyCapacity = 232

// BuddyGroupLimit is the longest group name in Big5 bytes. The v148 friend
// record (0x4C9AE0, 57 bytes) keeps the group at +24 in a 33-byte array.
const BuddyGroupLimit = 32

func ValidBuddyText(value string, limit int) bool {
	if strings.TrimSpace(value) == "" || strings.ContainsRune(value, 0) {
		return false
	}
	encoded, err := traditionalchinese.Big5.NewEncoder().Bytes([]byte(value))
	return err == nil && len(encoded) <= limit
}

var (
	ErrBuddyFull       = errors.New("buddy list full")
	ErrBuddyTargetFull = errors.New("target buddy list full")
)

type BuddyEntry struct {
	CharacterID int64  `json:"character_id"`
	Name        string `json:"name"`
	Group       string `json:"group"`
	Pending     bool   `json:"pending"`
	Level       int    `json:"level"`
	Job         int    `json:"job"`
	SubJob      int    `json:"sub_job"`
	// Channel is ephemeral and filled by the world process. Zero is offline.
	Channel int `json:"channel"`
}

type BuddyList struct {
	Capacity int          `json:"capacity"`
	Entries  []BuddyEntry `json:"entries"`
}

type BuddyRequest struct {
	Session  CharacterSession
	Action   uint8
	Name     string
	Group    string
	TargetID int64
}

type BuddyMutation struct {
	List         BuddyList
	TargetID     int64
	TargetList   BuddyList
	Invitation   bool
	SenderName   string
	SenderLevel  int
	SenderJob    int
	SenderSubJob int
}

type BuddyRepository interface {
	ReadBuddies(context.Context, CharacterSession) (BuddyList, error)
	MutateBuddy(context.Context, BuddyRequest) (BuddyMutation, error)
}
