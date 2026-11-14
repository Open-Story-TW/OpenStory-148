package domain

import "context"

// QAFamilyRequest turns an offline QA character into another job family,
// including the family's MakeCharInfo look and starter gear. It exists
// because the v145 client cannot create Mercedes, Demon or Phantom.
type QAFamilyRequest struct {
	OperationID string `json:"operation_id"`
	Actor       string `json:"-"`
	AccountID   int64  `json:"account_id"`
	CharacterID int64  `json:"character_id"`
	Job         int    `json:"job"`
	Reason      string `json:"reason"`
}

type QAFamilyResult struct {
	Character Character `json:"character"`
	Replayed  bool      `json:"replayed"`
}

type QAFamilyRepository interface {
	ApplyQAFamily(context.Context, QAFamilyRequest) (QAFamilyResult, error)
}

// JobFamily is the MakeCharInfo family a job belongs to (2003 Phantom has
// no MakeCharInfo entry in v145).
func JobFamily(job int) int {
	switch {
	case IsCygnusJob(job):
		return 1000
	case IsEvanJob(job):
		return 2001
	case IsMercedesJob(job):
		return 2002
	case IsPhantomJob(job):
		return 2003
	case IsAranJob(job):
		return 2000
	case IsDemonJob(job):
		return 3001
	case IsResistanceJob(job):
		return 3000
	}
	return 0
}
