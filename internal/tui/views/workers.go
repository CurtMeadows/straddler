package views

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/bubbles/spinner"

	"github.com/CurtMeadows/straddler/internal/config"
	"github.com/CurtMeadows/straddler/internal/db"
	"github.com/CurtMeadows/straddler/internal/registry"
	"github.com/CurtMeadows/straddler/internal/telemetry"
	"github.com/CurtMeadows/straddler/internal/tui/components"
	"github.com/CurtMeadows/straddler/internal/tui/msgs"
	"github.com/CurtMeadows/straddler/internal/tui/styles"
	"github.com/CurtMeadows/straddler/internal/worker"
)

const workersRefresh = 2 * time.Second

// WorkersModel shows in-progress jobs and lets the user start/stop the worker pool.
type WorkersModel struct {
	cfg        *config.Config
	queue      *db.Queue
	regClient  registry.Client
	inProgress []db.Job
	summary    *db.StatusSummary
	poolRunning bool
	cancelPool  context.CancelFunc
	spinner     spinner.Model
	confirm     components.Confirm
	width       int
	height      int
}

// NewWorkers creates a WorkersModel.
func NewWorkers(cfg *config.Config, queue *db.Queue, reg registry.Client) WorkersModel {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	return WorkersModel{
		cfg:       cfg,
		queue:     queue,
		regClient: reg,
		spinner:   sp,
	}
}

// SetSize updates the available rendering area.
func (m *WorkersModel) SetSize(w, h int) {
	m.width = w
	m.height = h
}

// StopPool cancels the running worker pool (called on TUI quit).
func (m *WorkersModel) StopPool() {
	if m.cancelPool != nil {
		m.cancelPool()
	}
}

// Init fetches in-progress jobs and starts the refresh ticker.
func (m WorkersModel) Init() tea.Cmd {
	return tea.Batch(
		fetchInProgressCmd(m.queue),
		fetchSummaryCmd(m.queue),
		tea.Tick(workersRefresh, func(t time.Time) tea.Msg { return msgs.TickMsg(t) }),
		m.spinner.Tick,
	)
}

// Update handles messages for the workers view.
func (m WorkersModel) Update(msg tea.Msg) (WorkersModel, tea.Cmd) {
	// Confirm dialog gets priority.
	if m.confirm.Visible {
		if cmd := m.confirm.Update(msg); cmd != nil {
			return m, cmd
		}
		return m, nil
	}

	switch msg := msg.(type) {
	case components.ConfirmYesMsg:
		if m.cancelPool != nil {
			m.cancelPool()
		}
		return m, nil

	case components.ConfirmNoMsg:
		return m, nil

	case msgs.TickMsg:
		return m, tea.Batch(
			fetchInProgressCmd(m.queue),
			fetchSummaryCmd(m.queue),
			tea.Tick(workersRefresh, func(t time.Time) tea.Msg { return msgs.TickMsg(t) }),
		)

	case msgs.InProgressJobsMsg:
		if msg.Err == nil {
			m.inProgress = msg.Jobs
		}
		return m, nil

	case msgs.StatusSummaryMsg:
		if msg.Err == nil {
			m.summary = msg.Summary
		}
		return m, nil

	case msgs.WorkerStoppedMsg:
		m.poolRunning = false
		m.cancelPool = nil
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "s":
			if m.poolRunning {
				m.confirm.Show("Stop the worker pool?")
				return m, nil
			}
			return m, m.startWorkerCmd()
		case "r":
			return m, tea.Batch(fetchInProgressCmd(m.queue), fetchSummaryCmd(m.queue))
		}

	default:
		var spCmd tea.Cmd
		m.spinner, spCmd = m.spinner.Update(msg)
		return m, spCmd
	}

	return m, nil
}

// View renders the workers view.
func (m WorkersModel) View() string {
	var sb strings.Builder

	// Status header.
	if m.poolRunning {
		sb.WriteString(m.spinner.View() + " ")
		sb.WriteString(styles.Bold.Render(fmt.Sprintf("Pool running (%d workers)", m.cfg.Worker.Concurrency)))
		sb.WriteString("   ")
		sb.WriteString(styles.KeyHintKey.Render("[S]") + styles.KeyHintDesc.Render("top pool"))
	} else {
		sb.WriteString(styles.Subtle.Render("Pool stopped"))
		sb.WriteString("   ")
		sb.WriteString(styles.KeyHintKey.Render("[S]") + styles.KeyHintDesc.Render("tart pool"))
	}
	sb.WriteString("   " + styles.KeyHintKey.Render("[R]") + styles.KeyHintDesc.Render("efresh"))
	sb.WriteString("\n\n")

	// In-progress jobs table.
	if len(m.inProgress) == 0 {
		sb.WriteString(styles.Subtle.Render("  No jobs currently in progress"))
	} else {
		header := fmt.Sprintf("  %-8s  %-40s  %-20s  %-8s  %s",
			"ID", "Source", "Claimed By", "Age", "Attempts")
		sb.WriteString(styles.TableHeader.Render(header))
		sb.WriteString("\n")
		sb.WriteString(strings.Repeat("─", m.width-2))
		sb.WriteString("\n")

		stale := m.cfg.Worker.StaleTimeout
		for _, j := range m.inProgress {
			age := time.Since(*j.ClaimedAt).Round(time.Second)
			claimedBy := ""
			if j.ClaimedBy != nil {
				claimedBy = *j.ClaimedBy
			}
			isStale := age > stale
			src := truncate(j.SourceRef, 40)
			line := fmt.Sprintf("  %-8d  %-40s  %-20s  %-8s  %d/%d",
				j.ID, src, truncate(claimedBy, 20), age, j.AttemptCount, j.MaxAttempts)
			if isStale {
				line += " " + styles.FormError.Render("(STALE)")
			}
			sb.WriteString(line + "\n")
		}
	}

	// Summary footer.
	if m.summary != nil {
		sb.WriteString("\n")
		sb.WriteString(strings.Repeat("─", m.width-2))
		sb.WriteString("\n")
		total := m.summary.Pending + m.summary.InProgress + m.summary.Completed + m.summary.Failed
		fmt.Fprintf(&sb, "  Pending: %d  In Progress: %d  Completed: %d  Failed: %d  Total: %d",
			m.summary.Pending, m.summary.InProgress, m.summary.Completed, m.summary.Failed, total)
	}

	// Confirm overlay.
	if m.confirm.Visible {
		sb.WriteString("\n\n")
		sb.WriteString(m.confirm.View(m.width))
	}

	return sb.String()
}

// startWorkerCmd launches the worker pool as a blocking tea.Cmd.
func (m *WorkersModel) startWorkerCmd() tea.Cmd {
	ctx, cancel := context.WithCancel(context.Background())
	m.cancelPool = cancel
	m.poolRunning = true

	cfg := m.cfg
	queue := m.queue
	regClient := m.regClient

	return func() tea.Msg {
		logger := telemetry.New(cfg.Log.Level, cfg.Log.Format)
		ctx = telemetry.WithLogger(ctx, logger)

		hostname, _ := os.Hostname()
		if hostname == "" {
			hostname = "tui-worker"
		}

		workerCfg := worker.WorkerConfig{
			WorkerID:     hostname,
			PollInterval: cfg.Worker.PollInterval,
			MaxAttempts:  cfg.Worker.MaxAttempts,
			BaseBackoff:  cfg.Worker.BaseBackoff,
		}

		p := worker.NewPool(cfg.Worker.Concurrency, cfg.Worker.StaleTimeout, workerCfg, queue, regClient)
		err := p.Run(ctx)
		return msgs.WorkerStoppedMsg{Err: err}
	}
}

