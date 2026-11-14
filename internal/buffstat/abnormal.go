package buffstat

// abnormal are the player debuffs a monster skill can inflict (Java
// MapleBuffStatus.getByMobSkill); Hero's Will style dispels remove them.
var abnormal = map[Stat]bool{
	"SEAL": true, "DARKNESS": true, "WEAKEN": true, "STUN": true, "CURSE": true, "POISON": true,
	"SLOW": true, "SEDUCE": true, "REVERSE_DIRECTION": true, "ZOMBIFY": true, "POTION": true,
	"SHADOW": true, "BLIND": true, "FREEZE": true,
}

// Abnormal reports whether stat is a dispellable player debuff.
func Abnormal(stat Stat) bool { return abnormal[stat] }
