package main

import (
	"log/slog"
	"maple-go-server/internal/admin"
	"maple-go-server/internal/app"
	"os"
)

func main() {
	if err := app.Run("admin", admin.Run); err != nil {
		slog.Error("service_failed", "service", "admin", "error", err)
		os.Exit(1)
	}
}
