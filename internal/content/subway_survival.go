package content

import (
	"fmt"
	"time"
)

const (
	SubwaySurvivalName    = "SubwaySurvival"
	SubwaySurvivalLobby   = 910320000
	SubwaySurvivalFailed  = 910320001
	SubwaySurvivalFirst   = 910320100
	SubwaySurvivalSecond  = 910320200
	SubwaySurvivalThird   = 910320300
	SubwaySurvivalCleared = 910330001
	SubwaySurvivalNPC     = 1052115
	SubwayBoardingPass    = 4001321
)

func SubwaySurvivalStage(mapID int) bool {
	switch mapID {
	case SubwaySurvivalFirst, SubwaySurvivalSecond, SubwaySurvivalThird:
		return true
	}
	return false
}

type SubwaySurvivalRules struct {
	Gauge, Decay, HitGain, CoolGain, MissCost int
	Respawn                                   time.Duration
	DisableSkills                             bool
}

func parseSubwaySurvival(node, info *wzNode) (*SubwaySurvivalRules, error) {
	gauge := node.child("gauge")
	p := &SubwaySurvivalRules{
		Gauge:         gauge.intValue("total", 0),
		Decay:         gauge.intValue("decrease", 0),
		HitGain:       gauge.intValue("hitAdd", 0),
		CoolGain:      gauge.intValue("coolAdd", 0),
		MissCost:      gauge.intValue("missSub", 0),
		Respawn:       time.Duration(info.intValue("createMobInterval", 0)) * time.Millisecond,
		DisableSkills: node.intValue("disableSkill", 0) != 0,
	}
	if info.intValue("fieldType", 0) != 23 || p.Gauge < 1 || p.Decay < 1 ||
		p.HitGain < 1 || p.CoolGain < 1 || p.MissCost < 1 || p.Respawn <= 0 || !p.DisableSkills {
		return nil, fmt.Errorf("invalid Subway survival rules")
	}
	return p, nil
}
