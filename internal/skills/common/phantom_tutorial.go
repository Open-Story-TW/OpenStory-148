package common

import "maple-go-server/internal/skills"

func init() {
	// 擾亂: the original guard tutorial performs the lure and guard response
	// locally. This registration permits normal acquisition and validates the
	// original zero MP cost, without installing a server buff or attack.
	skills.Register(skills.Handler{ID: 20031212, Kind: skills.KindSpecial, Cast: skills.CostOnly})
}
