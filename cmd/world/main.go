package main

import (
	"flag"
	"log/slog"
	"maple-go-server/internal/app"
	"maple-go-server/internal/combat"
	"maple-go-server/internal/skills"
	_ "maple-go-server/internal/skills/all"
	"maple-go-server/internal/world"
	"os"
	"strconv"
)

func main() {
	id := flag.Int("world-id", -1, "world ID from configured directory")
	flag.Parse()
	if *id >= 0 {
		os.Setenv("MAPLEGO_WORLD_ID", strconv.Itoa(*id))
	}
	// Attack damage uses passives and buffs, not equipment alone.
	combat.SetStatsProvider(skills.Stats)
	combat.SetNormalAttack(skills.NormalAttack)
	if err := app.Run("world", world.Run); err != nil {
		slog.Error("service_failed", "service", "world", "error", err)
		os.Exit(1)
	}
}
