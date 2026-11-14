package domain

import (
	"context"
	"errors"
)

type PotentialAction string

const (
	PotentialSeal    PotentialAction = "seal"
	PotentialMagnify PotentialAction = "magnify"
)

// A Commit error is not proof of rollback. The controller must ResolvePotential
// with this exact request, or close the old connection without retrying a roll.
var ErrPotentialOutcomeUnknown = errors.New("potential commit outcome is unknown")

type PotentialCommandRequest struct {
	Session         CharacterSession
	ClientTick      uint32
	Action          PotentialAction
	SourceSlot      int
	TargetSlot      int
	LegendarySpirit bool
}

type PotentialCommandResult struct {
	Inventory           InventoryResult    `json:"inventory"`
	Action              PotentialAction    `json:"action"`
	OperationID         string             `json:"operation_id"`
	SourceSlot          int                `json:"source_slot"`
	TargetSlot          int                `json:"target_slot"`
	ToolID              int                `json:"tool_id"`
	ToolInstanceID      int64              `json:"tool_instance_id"`
	EquipmentID         int64              `json:"equipment_id"`
	EquipmentTemplateID int                `json:"equipment_template_id"`
	Status              int                `json:"status"` // 0 failure or 1 success; no destruction in this family.
	LegendarySpirit     bool               `json:"legendary_spirit"`
	MesoFee             int64              `json:"meso_fee"`
	CommittedRevision   int64              `json:"committed_revision"`
	EquipmentBefore     Equipment          `json:"equipment_before"`
	BeforeStored        EquipmentPotential `json:"before_stored"`
	NativeBefore        EquipmentPotential `json:"native_before"`
	LogicalPolicyBefore EquipmentPotential `json:"logical_policy_before"`
	Normalization       string             `json:"normalization"`
	// Exact canonical content.PotentialPlan JSON bytes, encoded as a base64
	// scalar inside the JSONB receipt. JSONB key ordering must not change the
	// immutable transcript's bytes/hash. Domain must not import content.
	PlanJSON   []byte `json:"plan_json"`
	PlanSHA256 string `json:"plan_sha256"`
	Replayed   bool   `json:"replayed"`
}

type PotentialRepository interface {
	ApplyPotential(context.Context, PotentialCommandRequest) (PotentialCommandResult, error)
	// Same fencing/ordered SQL locks as Apply. This method never calls RNG,
	// consumes an item or re-executes the operation. found=false,nil means the
	// original transaction finished without a receipt under those locks.
	// A found receipt at a later character revision returns ErrStaleOwner.
	ResolvePotential(context.Context, PotentialCommandRequest) (PotentialCommandResult, bool, error)
}
