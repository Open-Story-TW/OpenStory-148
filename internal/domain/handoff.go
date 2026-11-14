package domain

import "context"

// OwnedSession is one durable account session claimed by a running world or
// cashshop instance. Recovery compares these rows with the process's live
// connections to find handoffs whose in-memory claim was lost.
type OwnedSession struct {
	AccountID, CharacterID int64
	SessionID              string
	Generation             int64
	ChannelID              int
}

// HandoffRecoveryRepository is optional: a world without it keeps the
// original single-attempt handoff behavior.
type HandoffRecoveryRepository interface {
	// ResumeChannelTransfer confirms that a FinishChannelTransfer whose result
	// was lost (timeout or connection error after commit) did commit, and
	// returns the session it produced. It never advances any state.
	ResumeChannelTransfer(context.Context, CharacterSession, int) (CharacterSession, error)
	ListWorldSessions(context.Context, WorldLease) ([]OwnedSession, error)
}

type CashHandoffRecoveryRepository interface {
	ListCashSessions(context.Context, ServiceLease) ([]OwnedSession, error)
}
