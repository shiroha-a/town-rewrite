// Package app wires the dependencies and runs the selected mode.
package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/shiroha-a/town/internal/action"
	"github.com/shiroha-a/town/internal/config"
	"github.com/shiroha-a/town/internal/content"
	"github.com/shiroha-a/town/internal/db"
	"github.com/shiroha-a/town/internal/httpapi"
	"github.com/shiroha-a/town/internal/ledger"
	"github.com/shiroha-a/town/internal/player"
	"github.com/shiroha-a/town/internal/rediscli"
	"github.com/shiroha-a/town/internal/rng"
	"github.com/shiroha-a/town/internal/worker"
)

// Run boots the given mode ("web" or "worker") with shared infrastructure.
func Run(ctx context.Context, mode string, cfg *config.Config) error {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	// マイグレーションは両モードで実行(冪等)。
	if err := db.Migrate(cfg.Database.URL); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	pool, err := db.Connect(ctx, cfg.Database.URL)
	if err != nil {
		return err
	}
	defer pool.Close()

	rdb, err := rediscli.Connect(ctx, cfg.Redis.Addr, cfg.Redis.DB)
	if err != nil {
		return err
	}
	defer rdb.Close()

	loc, err := time.LoadLocation(cfg.Game.Timezone)
	if err != nil {
		logger.Warn("invalid timezone, falling back to UTC", "timezone", cfg.Game.Timezone, "err", err)
		loc = time.UTC
	}

	led := ledger.New(pool)
	rnd := rng.New(cfg.Game.RNGSeed)
	players := player.New(pool, led, rnd, cfg.Game.InitialMoney, cfg.Game.DebugNoCooldown)
	actions := action.New(pool, led, players, rnd, loc, cfg.Game.DayBoundaryHour, cfg.Game.WorkIntervalMin,
		cfg.Game.EnergyRecoverySec, cfg.Game.NouRecoverySec, cfg.Game.DebugNoCooldown)
	contentSvc := content.New(pool)

	switch mode {
	case "web":
		return runWeb(ctx, cfg, logger, players, actions, contentSvc)
	case "worker":
		return worker.New(rdb, pool, led, cfg, logger).Run(ctx)
	default:
		return fmt.Errorf("unknown mode %q (want web|worker)", mode)
	}
}

func runWeb(ctx context.Context, cfg *config.Config, logger *slog.Logger, players *player.Service, actions *action.Service, contentSvc *content.Service) error {
	srv := &http.Server{
		Addr:              cfg.Server.HTTPAddr,
		Handler:           httpapi.NewServer(players, actions, contentSvc),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	logger.Info("http server listening", "addr", cfg.Server.HTTPAddr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("http serve: %w", err)
	}
	return nil
}
