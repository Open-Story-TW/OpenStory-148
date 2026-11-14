package main

import (
	"log/slog"
	"maple-go-server/internal/app"
	"maple-go-server/internal/cashshop"
	"os"
)

func main() {
	if err := app.Run("cashshop", cashshop.Run); err != nil {
		slog.Error("service_failed", "service", "cashshop", "error", err)
		os.Exit(1)
	}
}
