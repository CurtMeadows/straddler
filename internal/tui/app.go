// Package tui provides the interactive terminal dashboard for straddler.
package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/CurtMeadows/straddler/internal/config"
	"github.com/CurtMeadows/straddler/internal/db"
	"github.com/CurtMeadows/straddler/internal/registry"
	"github.com/CurtMeadows/straddler/internal/tui/components"
	"github.com/CurtMeadows/straddler/internal/tui/msgs"
	"github.com/CurtMeadows/straddler/internal/tui/styles"
	"github.com/CurtMeadows/straddler/internal/tui/views"
)

const (
	tabBarHeight  = 2
	statusBarHeight = 1
)

var tabLabels = []string{"Dashboard", "Jobs", "Sync", "Workers", "Migrate"}

// App is the root Bubbletea model.
type App struct {
	activeView int
	width      int
	height     int
	banner     components.Banner
	statusBar  components.StatusBar

	dashboard views.DashboardModel
	jobs      views.JobsModel
	sync      views.SyncModel
	workers   views.WorkersModel
	migrate   views.MigrateModel
}

// NewApp constructs the root App model.
func NewApp(cfg *config.Config, queue *db.Queue, reg registry.Client) App {
	return App{
		activeView: views.ViewDashboard,
		dashboard:  views.NewDashboard(queue),
		jobs:       views.NewJobs(queue),
		sync:       views.NewSync(cfg, queue, reg),
		workers:    views.NewWorkers(cfg, queue, reg),
		migrate:    views.NewMigrate(cfg.Database.DSN),
	}
}

// Init starts all child views.
func (a App) Init() tea.Cmd {
	return tea.Batch(
		a.dashboard.Init(),
		a.jobs.Init(),
		a.sync.Init(),
		a.workers.Init(),
		a.migrate.Init(),
	)
}

// Update handles all messages.
func (a App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.width = msg.Width
		a.height = msg.Height
		a.statusBar.Width = msg.Width
		availH := msg.Height - tabBarHeight - statusBarHeight
		if availH < 1 {
			availH = 1
		}
		a.dashboard.SetSize(msg.Width, availH)
		a.jobs.SetSize(msg.Width, availH)
		a.sync.SetSize(msg.Width, availH)
		a.workers.SetSize(msg.Width, availH)
		a.migrate.SetSize(msg.Width, availH)
		return a, nil

	case msgs.ErrorBannerMsg:
		a.banner.Set(msg.Text, true)
		return a, nil

	case msgs.ClearBannerMsg:
		a.banner.Clear()
		return a, nil

	case msgs.SwitchViewMsg:
		a.activeView = msg.View
		a.updateStatusBar()
		return a, nil

	case tea.KeyMsg:
		// Global navigation — only when no text input is active.
		switch msg.String() {
		case "q", "ctrl+c":
			a.workers.StopPool()
			return a, tea.Quit
		case "tab":
			a.activeView = (a.activeView + 1) % len(tabLabels)
			a.updateStatusBar()
			return a, nil
		case "shift+tab":
			a.activeView = (a.activeView - 1 + len(tabLabels)) % len(tabLabels)
			a.updateStatusBar()
			return a, nil
		case "1":
			a.activeView = views.ViewDashboard
			a.updateStatusBar()
			return a, nil
		case "2":
			a.activeView = views.ViewJobs
			a.updateStatusBar()
			return a, nil
		case "3":
			a.activeView = views.ViewSync
			a.updateStatusBar()
			return a, nil
		case "4":
			a.activeView = views.ViewWorkers
			a.updateStatusBar()
			return a, nil
		case "5":
			a.activeView = views.ViewMigrate
			a.updateStatusBar()
			return a, nil
		}
	}

	// Delegate to the active view.
	var cmd tea.Cmd
	switch a.activeView {
	case views.ViewDashboard:
		a.dashboard, cmd = a.dashboard.Update(msg)
	case views.ViewJobs:
		a.jobs, cmd = a.jobs.Update(msg)
	case views.ViewSync:
		a.sync, cmd = a.sync.Update(msg)
	case views.ViewWorkers:
		a.workers, cmd = a.workers.Update(msg)
	case views.ViewMigrate:
		a.migrate, cmd = a.migrate.Update(msg)
	}

	// Banner also handles ClearBannerMsg from any source.
	a.banner.Update(msg)

	return a, cmd
}

// View renders the full-screen TUI.
func (a App) View() string {
	tabBar := a.renderTabBar()
	content := a.renderActiveView()
	statusBar := a.statusBar.View()

	bannerStr := ""
	if a.banner.Visible {
		bannerStr = a.banner.View(a.width) + "\n"
	}

	return lipgloss.JoinVertical(lipgloss.Left,
		tabBar,
		bannerStr+content,
		statusBar,
	)
}

func (a App) renderTabBar() string {
	var parts []string
	for i, label := range tabLabels {
		if i == a.activeView {
			parts = append(parts, styles.TabActive.Render(label))
		} else {
			parts = append(parts, styles.TabInactive.Render(label))
		}
	}
	tabs := lipgloss.JoinHorizontal(lipgloss.Top, parts...)
	line := strings.Repeat("─", a.width)
	return tabs + "\n" + styles.Subtle.Render(line)
}

func (a App) renderActiveView() string {
	availH := a.height - tabBarHeight - statusBarHeight
	if a.banner.Visible {
		availH--
	}
	_ = availH // used for SetSize; individual views manage their own scrolling

	switch a.activeView {
	case views.ViewDashboard:
		return a.dashboard.View()
	case views.ViewJobs:
		return a.jobs.View()
	case views.ViewSync:
		return a.sync.View()
	case views.ViewWorkers:
		return a.workers.View()
	case views.ViewMigrate:
		return a.migrate.View()
	}
	return ""
}

func (a *App) updateStatusBar() {
	baseHints := []components.KeyHint{
		{Key: "Tab", Desc: "next view"},
		{Key: "1-5", Desc: "jump"},
		{Key: "Q", Desc: "quit"},
	}
	switch a.activeView {
	case views.ViewDashboard:
		a.statusBar.Hints = append([]components.KeyHint{{Key: "R", Desc: "refresh"}}, baseHints...)
	case views.ViewJobs:
		a.statusBar.Hints = append([]components.KeyHint{
			{Key: "/", Desc: "search"},
			{Key: "Tab", Desc: "filter"},
			{Key: "</>", Desc: "page"},
			{Key: "Enter", Desc: "detail"},
		}, baseHints[1:]...)
	case views.ViewSync:
		a.statusBar.Hints = append([]components.KeyHint{{Key: "Esc", Desc: "back"}}, baseHints...)
	case views.ViewWorkers:
		a.statusBar.Hints = append([]components.KeyHint{{Key: "S", Desc: "start/stop"}}, baseHints...)
	case views.ViewMigrate:
		a.statusBar.Hints = append([]components.KeyHint{
			{Key: "U", Desc: "migrate up"},
			{Key: "D", Desc: "migrate down"},
		}, baseHints...)
	default:
		a.statusBar.Hints = baseHints
	}
}
