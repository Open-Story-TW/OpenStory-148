package domain

import (
	"context"
	"errors"
	"time"
)

const PartyCapacity = 6

var (
	ErrPartyFull   = errors.New("party full")
	ErrPartyInvite = errors.New("invalid or expired party invitation")
)

type PartyAction uint8

const (
	PartyCreate      PartyAction = 1
	PartyLeave       PartyAction = 2
	PartyInvite      PartyAction = 4
	PartyExpel       PartyAction = 5
	PartyLeader      PartyAction = 6
	PartySetting     PartyAction = 8
	PartyAcknowledge PartyAction = 26
	PartyAccept      PartyAction = 27
	PartyPending     PartyAction = 28
	PartyDuplicate   PartyAction = 29
	PartyDeny        PartyAction = 30

	// Expedition actions. They never come from the party wire; the world
	// authorizes them against the expedition before asking the repository.
	// PartyJoin adds the actor to PartyID without an invitation. PartyRemove
	// takes TargetID out of PartyID and promotes the next member when the
	// leader goes. PartySetLeader makes TargetID PartyID's leader.
	// PartyDisband deletes PartyID.
	PartyJoin      PartyAction = 200
	PartyRemove    PartyAction = 201
	PartySetLeader PartyAction = 202
	PartyDisband   PartyAction = 203
)

type PartyMember struct {
	CharacterID int64
	Name        string
	Slot        int
	Level, Job  int
	// SubJob is the character subcategory (dual blade, cannoneer) that the
	// v148 party record carries next to the job.
	SubJob int
	// Runtime fields are deliberately absent from SQL. Offline members have
	// Channel=0; the world overlays only its current, identity-checked peers.
	Channel, MapID, HP, MaxHP int
}

type PartySnapshot struct {
	ID       int64
	WorldID  int
	LeaderID int64
	Revision int64
	Setting  bool
	Members  []PartyMember
}

func (p PartySnapshot) Member(id int64) (PartyMember, bool) {
	for _, m := range p.Members {
		if m.CharacterID == id {
			return m, true
		}
	}
	return PartyMember{}, false
}

type PartyInvitation struct {
	PartyID, InviterID, TargetID int64
	InviterName                  string
	InviterLevel, InviterJob     int
	InviterSubJob                int
	ExpiresAt                    time.Time
}

type PartyState struct {
	Party       PartySnapshot
	Invitations []PartyInvitation
}

type PartyRequest struct {
	Session           CharacterSession
	Action            PartyAction
	PartyID, TargetID int64
	Name              string
	Setting           bool
}

type PartyMutation struct {
	Action        PartyAction
	Before, After PartySnapshot
	TargetID      int64
	Invitation    *PartyInvitation
	Created       bool
}

type PartyRepository interface {
	ReadParty(context.Context, CharacterSession) (PartyState, error)
	MutateParty(context.Context, PartyRequest) (PartyMutation, error)
}
