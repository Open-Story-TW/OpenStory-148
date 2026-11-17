package domain

import (
	"context"
	"strconv"
)

const MonsterRushSchedulePrefix = "monster-rush-start-"

func MonsterRushLogKeys(channel int) (ScriptLogKey, ScriptLogKey) {
	suffix := strconv.Itoa(channel)
	return ScriptLogKey{Scope: LogWorld, Period: LogPermanent, Name: "monster-rush-fund-" + suffix},
		ScriptLogKey{Scope: LogWorld, Period: LogPermanent, Name: MonsterRushSchedulePrefix + suffix}
}

type MonsterRushScheduleRepository interface {
	MonsterRushSchedules(context.Context, int) (map[int]int64, error)
}
