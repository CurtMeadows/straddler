// Package msgs defines all Bubbletea message types used by the TUI.
package msgs

import (
	"time"

	"github.com/CurtMeadows/straddler/internal/db"
)

// TickMsg is sent by the periodic refresh timer.
type TickMsg time.Time

// StatusSummaryMsg carries the result of a StatusSummaryFor DB call.
type StatusSummaryMsg struct {
	Summary *db.StatusSummary
	Err     error
}

// JobsLoadedMsg carries a page of jobs.
type JobsLoadedMsg struct {
	Page *db.JobPage
	Err  error
}

// RecentJobsMsg carries the most recent jobs for the dashboard feed.
type RecentJobsMsg struct {
	Jobs []db.Job
	Err  error
}

// JobDetailMsg carries one fully-loaded job row.
type JobDetailMsg struct {
	Job *db.Job
	Err error
}

// SyncEnqueuedMsg carries the result of BulkEnqueue from the sync wizard.
type SyncEnqueuedMsg struct {
	Enqueued int64
	Skipped  int64
	Err      error
}

// TagsListedMsg carries tags fetched from the source registry.
type TagsListedMsg struct {
	Source string
	Tags   []string
	Err    error
}

// InProgressJobsMsg carries the in-progress job list for the workers view.
type InProgressJobsMsg struct {
	Jobs []db.Job
	Err  error
}

// WorkerStoppedMsg signals the pool exited.
type WorkerStoppedMsg struct{ Err error }

// MigrateVersionMsg carries the current schema version.
type MigrateVersionMsg struct {
	Version uint
	Dirty   bool
	Err     error
}

// MigrateDoneMsg carries the result of running a migration.
type MigrateDoneMsg struct {
	Direction string
	Err       error
}

// ErrorBannerMsg triggers the global error banner.
type ErrorBannerMsg struct{ Text string }

// ClearBannerMsg dismisses the global error banner.
type ClearBannerMsg struct{}

// SwitchViewMsg is an internal message that tells the App to switch views.
type SwitchViewMsg struct{ View int }
