package views

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/CurtMeadows/straddler/internal/db"
	"github.com/CurtMeadows/straddler/internal/tui/msgs"
)

const dbQueryTimeout = 5 * time.Second

func fetchSummaryCmd(q *db.Queue) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
		defer cancel()
		s, err := q.StatusSummaryFor(ctx, "")
		return msgs.StatusSummaryMsg{Summary: s, Err: err}
	}
}

func fetchInProgressCmd(q *db.Queue) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
		defer cancel()
		jobs, err := q.ListInProgress(ctx)
		return msgs.InProgressJobsMsg{Jobs: jobs, Err: err}
	}
}

// truncate shortens s to at most n characters, appending "…" if cut.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
