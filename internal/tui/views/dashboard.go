// Package views contains the individual screen models for the straddler TUI.
package views

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/CurtMeadows/straddler/internal/db"
	"github.com/CurtMeadows/straddler/internal/tui/msgs"
	"github.com/CurtMeadows/straddler/internal/tui/styles"
)

const dashboardRefresh = 5 * time.Second

// DashboardModel is the home screen showing live queue statistics.
type DashboardModel struct {
	queue       *db.Queue
	summary     *db.StatusSummary
	recentJobs  []db.Job
	lastRefresh time.Time
	loading     bool
	width       int
	height      int
}

// NewDashboard creates a DashboardModel.
func NewDashboard(queue *db.Queue) DashboardModel {
	return DashboardModel{queue: queue, loading: true}
}

// SetSize updates the available rendering area.
func (m *DashboardModel) SetSize(w, h int) {
	m.width = w
	m.height = h
}

// Init fires the initial data load and starts the refresh ticker.
func (m DashboardModel) Init() tea.Cmd {
	return tea.Batch(
		fetchSummaryCmd(m.queue),
		fetchRecentJobsCmd(m.queue),
		tea.Tick(dashboardRefresh, func(t time.Time) tea.Msg { return msgs.TickMsg(t) }),
	)
}

// Update handles messages for the dashboard.
func (m DashboardModel) Update(msg tea.Msg) (DashboardModel, tea.Cmd) {
	switch msg := msg.(type) {
	case msgs.TickMsg:
		return m, tea.Batch(
			fetchSummaryCmd(m.queue),
			fetchRecentJobsCmd(m.queue),
			tea.Tick(dashboardRefresh, func(t time.Time) tea.Msg { return msgs.TickMsg(t) }),
		)

	case msgs.StatusSummaryMsg:
		if msg.Err != nil {
			return m, func() tea.Msg { return msgs.ErrorBannerMsg{Text: "DB error: " + msg.Err.Error()} }
		}
		m.summary = msg.Summary
		m.lastRefresh = time.Now()
		m.loading = false
		return m, nil

	case msgs.RecentJobsMsg:
		if msg.Err == nil {
			m.recentJobs = msg.Jobs
		}
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "r":
			m.loading = true
			return m, tea.Batch(fetchSummaryCmd(m.queue), fetchRecentJobsCmd(m.queue))
		case "s":
			return m, func() tea.Msg { return msgs.SwitchViewMsg{View: ViewSync} }
		case "w":
			return m, func() tea.Msg { return msgs.SwitchViewMsg{View: ViewWorkers} }
		case "j":
			return m, func() tea.Msg { return msgs.SwitchViewMsg{View: ViewJobs} }
		case "m":
			return m, func() tea.Msg { return msgs.SwitchViewMsg{View: ViewMigrate} }
		}
	}
	return m, nil
}

// View renders the dashboard.
func (m DashboardModel) View() string {
	if m.loading && m.summary == nil {
		return "\n  " + styles.Subtle.Render("Loading…")
	}

	var sb strings.Builder

	// Stat tiles.
	tiles := m.renderTiles()
	sb.WriteString(lipgloss.PlaceHorizontal(m.width, lipgloss.Left, tiles))
	sb.WriteString("\n\n")

	// Recent activity header.
	sb.WriteString(styles.SectionHeader.Render("Recent Activity"))
	sb.WriteString("\n")

	if len(m.recentJobs) == 0 {
		sb.WriteString(styles.Subtle.Render("  No jobs yet"))
	} else {
		for _, j := range m.recentJobs {
			icon := styles.StatusStyle(string(j.Status)).Render(styles.StatusIcon(string(j.Status)))
			src := truncate(j.SourceRef, 50)
			ts := j.UpdatedAt.Format("15:04:05")
			line := fmt.Sprintf("  %s  %-52s  %s", icon, src, styles.Subtle.Render(ts))
			sb.WriteString(line + "\n")
		}
	}

	// Refresh time + shortcut hints.
	sb.WriteString("\n")
	if !m.lastRefresh.IsZero() {
		hint := styles.Subtle.Render(fmt.Sprintf("Refreshed %s", m.lastRefresh.Format("15:04:05")))
		shortcuts := "  " + styles.KeyHintKey.Render("[S]") + styles.KeyHintDesc.Render("ync") +
			"  " + styles.KeyHintKey.Render("[W]") + styles.KeyHintDesc.Render("orkers") +
			"  " + styles.KeyHintKey.Render("[J]") + styles.KeyHintDesc.Render("obs") +
			"  " + styles.KeyHintKey.Render("[M]") + styles.KeyHintDesc.Render("igrate") +
			"  " + styles.KeyHintKey.Render("[R]") + styles.KeyHintDesc.Render("efresh")
		sb.WriteString(lipgloss.JoinHorizontal(lipgloss.Bottom, shortcuts, "  ", hint))
	}

	return sb.String()
}

func (m DashboardModel) renderTiles() string {
	var (
		pending    int64
		inProgress int64
		completed  int64
		failed     int64
	)
	if m.summary != nil {
		pending = m.summary.Pending
		inProgress = m.summary.InProgress
		completed = m.summary.Completed
		failed = m.summary.Failed
	}

	tiles := []struct {
		label string
		value int64
		style lipgloss.Style
	}{
		{"PENDING", pending, lipgloss.NewStyle().Foreground(styles.StatusPending)},
		{"IN PROGRESS", inProgress, lipgloss.NewStyle().Foreground(styles.StatusInProgress)},
		{"COMPLETED", completed, lipgloss.NewStyle().Foreground(styles.StatusCompleted)},
		{"FAILED", failed, lipgloss.NewStyle().Foreground(styles.StatusFailed)},
	}

	var rendered []string
	for _, t := range tiles {
		label := styles.TileLabel.Render(t.label)
		value := t.style.Bold(true).Render(fmt.Sprintf("%d", t.value))
		tile := styles.Tile.Render(label + "\n" + value)
		rendered = append(rendered, tile)
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, rendered...)
}

func fetchSummaryCmd(q *db.Queue) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s, err := q.StatusSummaryFor(ctx, "")
		return msgs.StatusSummaryMsg{Summary: s, Err: err}
	}
}

func fetchRecentJobsCmd(q *db.Queue) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		page, err := q.ListJobs(ctx, db.ListJobsParams{Page: 0, PageSize: 10})
		if err != nil {
			return msgs.RecentJobsMsg{Err: err}
		}
		return msgs.RecentJobsMsg{Jobs: page.Jobs}
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
