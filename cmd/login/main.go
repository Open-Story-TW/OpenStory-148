package main

import (
	"log/slog"
	"maple-go-server/internal/app"
	"maple-go-server/internal/login"
	"os"
)

func main() {
	if err := app.Run("login", login.Run); err != nil {
		slog.Error("service_failed", "service", "login", "error", err)
		os.Exit(1)
	}
}
