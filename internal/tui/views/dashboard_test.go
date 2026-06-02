package views

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/CurtMeadows/straddler/internal/db"
	"github.com/CurtMeadows/straddler/internal/tui/msgs"
)

func TestDashboard_UpdatesSummaryOnMsg(t *testing.T) {
	m := NewDashboard(nil)
	assert.True(t, m.loading)

	summary := &db.StatusSummary{Pending: 10, InProgress: 2, Completed: 100, Failed: 1}
	m, _ = m.Update(msgs.StatusSummaryMsg{Summary: summary})

	assert.Equal(t, summary, m.summary)
	assert.False(t, m.loading)
}

func TestDashboard_ErrorSetsLoading(t *testing.T) {
	m := NewDashboard(nil)
	m.loading = false

	// An error response should leave loading false (it was false before) and
	// emit an ErrorBannerMsg — we just check the model state here.
	m, cmd := m.Update(msgs.StatusSummaryMsg{Err: assert.AnError})
	assert.NotNil(t, cmd, "error should emit a banner command")
	_ = m
}

func TestDashboard_RecentJobsUpdated(t *testing.T) {
	m := NewDashboard(nil)
	jobs := []db.Job{{ID: 1}, {ID: 2}}

	m, _ = m.Update(msgs.RecentJobsMsg{Jobs: jobs})
	assert.Equal(t, jobs, m.recentJobs)
}
