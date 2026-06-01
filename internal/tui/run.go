package tui

import (
	"context"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/CurtMeadows/straddler/internal/config"
	"github.com/CurtMeadows/straddler/internal/db"
	"github.com/CurtMeadows/straddler/internal/registry"
)

// Run opens the database, builds shared resources, and launches the TUI.
func Run(cfg *config.Config) error {
	ctx := context.Background()

	pool, err := db.Open(ctx,
		cfg.Database.DSN,
		cfg.Database.MaxConns,
		cfg.Database.MinConns,
		cfg.Database.ConnectTimeout,
	)
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}
	defer pool.Close()

	transport := registry.BuildTransport(cfg.Registry.InsecureSkipTLS)
	regClient := registry.NewRemoteClient(
		registry.BuildKeychain(cfg.Registry.Source),
		registry.BuildKeychain(cfg.Registry.Dest),
		transport,
	)

	queue := db.NewQueue(pool)
	app := NewApp(cfg, queue, regClient)

	p := tea.NewProgram(
		app,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)
	_, err = p.Run()
	return err
}
