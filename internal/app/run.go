package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/persistence"
	"maple-go-server/internal/platform"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/settings"
)

type Service func(context.Context, platform.Config, domain.Repository, *platform.Health) error

func Run(kind string, run Service) error {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	cfg, err := platform.Load(kind)
	if err != nil {
		return err
	}
	// A malformed launcher key must stop the service, not fall back to none.
	launcherKey := "off"
	if kind != "admin" {
		configured, keyErr := protocol.LoadLauncherKey()
		if keyErr != nil {
			return keyErr
		}
		if configured {
			launcherKey = "accepted"
			if os.Getenv(protocol.LauncherKeyRequiredEnv) == "1" {
				launcherKey = "required"
			}
		}
	}
	// A public server must only talk to its own launcher, over the encrypted
	// channel the key exchange sets up.
	if cfg.Public && kind != "admin" && launcherKey != "required" {
		return fmt.Errorf("MAPLEGO_DEPLOY=public needs %s and %s=1", protocol.LauncherKeyEnv, protocol.LauncherKeyRequiredEnv)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if cfg.StopFile != "" {
		go func() {
			ticker := time.NewTicker(250 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					if _, err := os.Stat(cfg.StopFile); err == nil {
						cancel()
						return
					}
				}
			}
		}()
	}
	openCtx, openCancel := context.WithTimeout(ctx, 10*time.Second)
	store, err := persistence.Open(openCtx, persistence.Config{DatabaseURL: cfg.DatabaseURL, RedisAddr: cfg.RedisAddr, RedisPassword: cfg.RedisPassword, RedisDB: cfg.RedisDB, MaxOpenConns: poolLimit(kind)})
	openCancel()
	if err != nil {
		return err
	}
	defer store.Close()
	settingsCtx, settingsCancel := context.WithTimeout(ctx, 5*time.Second)
	settings.Start(ctx, settingsCtx, store.Database())
	settingsCancel()
	health := platform.NewHealth(cfg, store.Ping)
	health.DetailFunc("settings", func() any { return settings.Active() })
	health.Detail("launcher_key", launcherKey)
	if err = health.Serve(ctx); err != nil {
		return err
	}
	slog.Info("service_start", "service", kind, "pid", os.Getpid(), "instance", cfg.InstanceID, "health", cfg.HealthAddr)
	err = run(ctx, cfg, store, health)
	health.Ready(false)
	if errors.Is(err, context.Canceled) {
		return nil
	}
	slog.Info("service_stop", "service", kind, "pid", os.Getpid())
	return err
}

func poolLimit(kind string) int {
	switch kind {
	case "login":
		return 8
	case "cashshop":
		return 16
	default:
		return 24
	}
}
