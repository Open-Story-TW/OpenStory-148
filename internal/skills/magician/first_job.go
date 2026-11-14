package magician

import "maple-go-server/internal/skills"

func init() {
	passive(2000006, nil)                                                                               // MP Increase: content/vitals.go until the stats provider lands
	skills.Register(skills.Handler{ID: 2001002, Kind: skills.KindBuff, Cast: buff("MAGIC_GUARD", "x")}) // Magic Guard
	skills.Register(skills.Handler{ID: 2001003, Kind: skills.KindBuff, Cast: skills.StandardBuff})      // Magic Armor: pdd, mdd
	magic(2001004)                                                                                      // Energy Bolt
	magic(2001005)                                                                                      // Magic Claw
}
