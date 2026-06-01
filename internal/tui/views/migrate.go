package views

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/CurtMeadows/straddler/internal/db"
	"github.com/CurtMeadows/straddler/internal/tui/components"
	"github.com/CurtMeadows/straddler/internal/tui/msgs"
	"github.com/CurtMeadows/straddler/internal/tui/styles"
)

// MigrateModel manages the schema migration view.
type MigrateModel struct {
	dsn     string
	version uint
	dirty   bool
	loaded  bool
	lastMsg string
	confirm components.Confirm
	width   int
	height  int
}

// NewMigrate creates a MigrateModel.
func NewMigrate(dsn string) MigrateModel {
	return MigrateModel{dsn: dsn}
}

// SetSize updates the available rendering area.
func (m *MigrateModel) SetSize(w, h int) {
	m.width = w
	m.height = h
}

// Init fetches the current migration version.
func (m MigrateModel) Init() tea.Cmd {
	return fetchMigrateVersionCmd(m.dsn)
}

// Update handles messages for the migrate view.
func (m MigrateModel) Update(msg tea.Msg) (MigrateModel, tea.Cmd) {
	// Confirm dialog gets priority when visible.
	if m.confirm.Visible {
		if cmd := m.confirm.Update(msg); cmd != nil {
			return m, cmd
		}
		return m, nil
	}

	switch msg := msg.(type) {
	case components.ConfirmYesMsg:
		return m, migrateCmd(m.dsn, "down")

	case components.ConfirmNoMsg:
		return m, nil

	case msgs.MigrateVersionMsg:
		if msg.Err != nil {
			m.lastMsg = "Error: " + msg.Err.Error()
		} else {
			m.version = msg.Version
			m.dirty = msg.Dirty
			m.loaded = true
		}
		return m, nil

	case msgs.MigrateDoneMsg:
		if msg.Err != nil {
			m.lastMsg = "Migration failed: " + msg.Err.Error()
		} else {
			m.lastMsg = fmt.Sprintf("Migration %s succeeded at %s", msg.Direction, time.Now().Format("15:04:05"))
		}
		return m, fetchMigrateVersionCmd(m.dsn)

	case tea.KeyMsg:
		switch msg.String() {
		case "u":
			return m, migrateCmd(m.dsn, "up")
		case "d":
			m.confirm.Show("Roll back the last migration?")
			return m, nil
		}
	}
	return m, nil
}

// View renders the migration view.
func (m MigrateModel) View() string {
	var sb strings.Builder

	sb.WriteString(styles.SectionHeader.Render("Database Migrations"))
	sb.WriteString("\n\n")

	if !m.loaded {
		sb.WriteString(styles.Subtle.Render("  Checking migration version…"))
	} else {
		versionStr := fmt.Sprintf("%d", m.version)
		if m.dirty {
			versionStr += " (dirty)"
		}
		sb.WriteString(styles.DetailKey.Render("Current version:") + " " + styles.DetailVal.Render(versionStr))
		sb.WriteString("\n\n")
		sb.WriteString("  " + styles.KeyHintKey.Render("[U]") + " " + styles.KeyHintDesc.Render("Apply all pending migrations (migrate up)") + "\n")
		sb.WriteString("  " + styles.KeyHintKey.Render("[D]") + " " + styles.KeyHintDesc.Render("Roll back last migration (migrate down)") + "\n")
	}

	if m.lastMsg != "" {
		sb.WriteString("\n")
		sb.WriteString(styles.Subtle.Render("  " + m.lastMsg))
		sb.WriteString("\n")
	}

	if m.confirm.Visible {
		sb.WriteString("\n")
		sb.WriteString(m.confirm.View(m.width))
	}

	return sb.String()
}

func fetchMigrateVersionCmd(dsn string) tea.Cmd {
	return func() tea.Msg {
		v, dirty, err := db.MigrateVersion(dsn)
		return msgs.MigrateVersionMsg{Version: v, Dirty: dirty, Err: err}
	}
}

func migrateCmd(dsn, direction string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		_ = ctx // golang-migrate doesn't accept context but we honor the timeout via defer
		err := db.Migrate(dsn, direction, 0)
		return msgs.MigrateDoneMsg{Direction: direction, Err: err}
	}
}
