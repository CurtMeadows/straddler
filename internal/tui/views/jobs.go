package views

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/lipgloss"

	"github.com/CurtMeadows/straddler/internal/db"
	"github.com/CurtMeadows/straddler/internal/tui/msgs"
	"github.com/CurtMeadows/straddler/internal/tui/styles"
)

const jobsPageSize = 20

// statusFilters lists the filter options cycled by Tab in the Jobs view.
var statusFilters = []string{"", "pending", "in_progress", "completed", "failed"}
var statusFilterLabels = []string{"All", "Pending", "In Progress", "Completed", "Failed"}

// JobsModel shows a paginated, filterable table of sync jobs.
type JobsModel struct {
	queue       *db.Queue
	tbl         table.Model
	jobs        []db.Job
	total       int64
	page        int
	filterIdx   int // index into statusFilters
	searching   bool
	searchInput textinput.Model
	detail      *db.Job
	width       int
	height      int
}

// NewJobs creates a JobsModel.
func NewJobs(queue *db.Queue) JobsModel {
	cols := []table.Column{
		{Title: "ID", Width: 8},
		{Title: "Source", Width: 32},
		{Title: "Dest", Width: 26},
		{Title: "Status", Width: 12},
		{Title: "Att", Width: 5},
		{Title: "Updated", Width: 10},
	}

	tableStyles := table.DefaultStyles()
	tableStyles.Header = tableStyles.Header.
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(styles.ColorBorder).
		BorderBottom(true).
		Bold(true).
		Foreground(styles.ColorMuted)
	tableStyles.Selected = tableStyles.Selected.
		Foreground(styles.ColorPrimary).
		Background(styles.ColorHighlight).
		Bold(false)

	tbl := table.New(
		table.WithColumns(cols),
		table.WithFocused(true),
		table.WithStyles(tableStyles),
	)

	si := textinput.New()
	si.Placeholder = "search source or dest…"
	si.CharLimit = 100

	return JobsModel{
		queue:       queue,
		tbl:         tbl,
		searchInput: si,
	}
}

// SetSize updates the available rendering area.
func (m *JobsModel) SetSize(w, h int) {
	m.width = w
	m.height = h
	tableHeight := h - 8 // leave room for filter bar, search, pagination, status
	if tableHeight < 5 {
		tableHeight = 5
	}
	m.tbl.SetHeight(tableHeight)
}

// Init fires the initial data load.
func (m JobsModel) Init() tea.Cmd {
	return fetchJobsCmd(m.queue, m.statusFilter(), m.searchQuery(), m.page)
}

// Update handles messages for the jobs view.
func (m JobsModel) Update(msg tea.Msg) (JobsModel, tea.Cmd) {
	// Search input intercepts keypresses when active.
	if m.searching {
		switch msg := msg.(type) {
		case tea.KeyMsg:
			switch msg.String() {
			case "enter":
				m.searching = false
				m.page = 0
				return m, fetchJobsCmd(m.queue, m.statusFilter(), m.searchQuery(), m.page)
			case "esc":
				m.searching = false
				m.searchInput.SetValue("")
				m.page = 0
				return m, fetchJobsCmd(m.queue, m.statusFilter(), m.searchQuery(), m.page)
			default:
				var cmd tea.Cmd
				m.searchInput, cmd = m.searchInput.Update(msg)
				return m, cmd
			}
		default:
			var cmd tea.Cmd
			m.searchInput, cmd = m.searchInput.Update(msg)
			return m, cmd
		}
	}

	// Detail pane active.
	if m.detail != nil {
		if km, ok := msg.(tea.KeyMsg); ok && km.String() == "esc" {
			m.detail = nil
			return m, nil
		}
		return m, nil
	}

	switch msg := msg.(type) {
	case msgs.JobsLoadedMsg:
		if msg.Err != nil {
			return m, func() tea.Msg { return msgs.ErrorBannerMsg{Text: "DB error: " + msg.Err.Error()} }
		}
		m.total = msg.Page.Total
		m.jobs = msg.Page.Jobs
		m.tbl.SetRows(jobsToRows(msg.Page.Jobs))
		return m, nil

	case msgs.JobDetailMsg:
		if msg.Err == nil {
			m.detail = msg.Job
		}
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "enter":
			row := m.tbl.SelectedRow()
			if len(row) > 0 {
				var id int64
				if _, err := fmt.Sscanf(row[0], "%d", &id); err != nil {
					return m, nil
				}
				return m, fetchJobDetailCmd(m.queue, id)
			}
		case "esc":
			m.detail = nil
		case "/":
			m.searching = true
			m.searchInput.Focus()
			return m, textinput.Blink
		case "tab":
			m.filterIdx = (m.filterIdx + 1) % len(statusFilters)
			m.page = 0
			return m, fetchJobsCmd(m.queue, m.statusFilter(), m.searchQuery(), m.page)
		case "shift+tab":
			m.filterIdx = (m.filterIdx - 1 + len(statusFilters)) % len(statusFilters)
			m.page = 0
			return m, fetchJobsCmd(m.queue, m.statusFilter(), m.searchQuery(), m.page)
		case "r":
			return m, fetchJobsCmd(m.queue, m.statusFilter(), m.searchQuery(), m.page)
		case "<", "h":
			if m.page > 0 {
				m.page--
				return m, fetchJobsCmd(m.queue, m.statusFilter(), m.searchQuery(), m.page)
			}
		case ">", "l":
			maxPage := int(m.total-1) / jobsPageSize
			if m.page < maxPage {
				m.page++
				return m, fetchJobsCmd(m.queue, m.statusFilter(), m.searchQuery(), m.page)
			}
		default:
			var cmd tea.Cmd
			m.tbl, cmd = m.tbl.Update(msg)
			return m, cmd
		}
	default:
		var cmd tea.Cmd
		m.tbl, cmd = m.tbl.Update(msg)
		return m, cmd
	}
	return m, nil
}

// View renders the jobs view.
func (m JobsModel) View() string {
	if m.detail != nil {
		return m.renderDetail()
	}

	var sb strings.Builder

	// Filter tabs.
	var filterParts []string
	for i, label := range statusFilterLabels {
		if i == m.filterIdx {
			filterParts = append(filterParts, styles.TabActive.Render(label))
		} else {
			filterParts = append(filterParts, styles.TabInactive.Render(label))
		}
	}
	sb.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, filterParts...))
	sb.WriteString("\n")

	// Search bar.
	if m.searching {
		sb.WriteString("  / " + m.searchInput.View() + "\n")
	} else if q := m.searchQuery(); q != "" {
		sb.WriteString(styles.Subtle.Render("  search: "+q) + "\n")
	}

	// Table.
	sb.WriteString(m.tbl.View())
	sb.WriteString("\n")

	// Pagination.
	totalPages := 1
	if m.total > 0 {
		totalPages = (int(m.total) + jobsPageSize - 1) / jobsPageSize
	}
	pager := fmt.Sprintf("  Page %d/%d  (%d total jobs)",
		m.page+1, totalPages, m.total)
	hints := "  " + styles.KeyHintKey.Render("[/]") + styles.KeyHintDesc.Render("search") +
		"  " + styles.KeyHintKey.Render("[Tab]") + styles.KeyHintDesc.Render("filter") +
		"  " + styles.KeyHintKey.Render("[</>]") + styles.KeyHintDesc.Render("page") +
		"  " + styles.KeyHintKey.Render("[Enter]") + styles.KeyHintDesc.Render("detail") +
		"  " + styles.KeyHintKey.Render("[R]") + styles.KeyHintDesc.Render("efresh")
	sb.WriteString(lipgloss.JoinHorizontal(lipgloss.Bottom,
		styles.Subtle.Render(pager), "  ", hints))

	return sb.String()
}

func (m JobsModel) renderDetail() string {
	j := m.detail
	var sb strings.Builder

	sb.WriteString(styles.SectionHeader.Render("Job Detail"))
	sb.WriteString("\n")
	sb.WriteString(styles.KeyHintDesc.Render("  [Esc] back to list"))
	sb.WriteString("\n\n")

	field := func(k, v string) {
		sb.WriteString(styles.DetailKey.Render(k) + "  " + styles.DetailVal.Render(v) + "\n")
	}
	field("ID", fmt.Sprintf("%d", j.ID))
	field("Source", j.SourceRef)
	field("Dest", j.DestRef)
	field("Status", styles.StatusStyle(string(j.Status)).Render(string(j.Status)))
	field("Attempts", fmt.Sprintf("%d / %d", j.AttemptCount, j.MaxAttempts))
	field("Created", j.CreatedAt.Format(time.RFC3339))
	field("Updated", j.UpdatedAt.Format(time.RFC3339))
	if j.ClaimedAt != nil {
		field("Claimed At", j.ClaimedAt.Format(time.RFC3339))
	}
	if j.ClaimedBy != nil {
		field("Claimed By", *j.ClaimedBy)
	}
	if j.CompletedAt != nil {
		field("Completed", j.CompletedAt.Format(time.RFC3339))
	}
	if j.LastError != nil && *j.LastError != "" {
		sb.WriteString("\n")
		sb.WriteString(styles.FormError.Render("Last Error:") + "\n")
		sb.WriteString(styles.DetailVal.Render("  " + *j.LastError) + "\n")
	}

	return sb.String()
}

func (m JobsModel) statusFilter() string {
	return statusFilters[m.filterIdx]
}

func (m JobsModel) searchQuery() string {
	return m.searchInput.Value()
}

func jobsToRows(jobs []db.Job) []table.Row {
	rows := make([]table.Row, len(jobs))
	for i, j := range jobs {
		rows[i] = table.Row{
			fmt.Sprintf("%d", j.ID),
			truncate(j.SourceRef, 32),
			truncate(j.DestRef, 26),
			string(j.Status),
			fmt.Sprintf("%d/%d", j.AttemptCount, j.MaxAttempts),
			j.UpdatedAt.Format("01-02 15:04"),
		}
	}
	return rows
}

func fetchJobsCmd(q *db.Queue, status, search string, page int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		p, err := q.ListJobs(ctx, db.ListJobsParams{
			Status:   status,
			Search:   search,
			Page:     page,
			PageSize: jobsPageSize,
		})
		return msgs.JobsLoadedMsg{Page: p, Err: err}
	}
}

func fetchJobDetailCmd(q *db.Queue, id int64) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		j, err := q.GetJob(ctx, id)
		return msgs.JobDetailMsg{Job: j, Err: err}
	}
}
