// Package styles defines shared Lipgloss styles for the straddler TUI.
package styles

import "github.com/charmbracelet/lipgloss"

var (
	// Color palette.
	ColorPrimary   = lipgloss.AdaptiveColor{Light: "#1a1a2e", Dark: "#c9d1d9"}
	ColorMuted     = lipgloss.AdaptiveColor{Light: "#6e7681", Dark: "#8b949e"}
	ColorAccent    = lipgloss.AdaptiveColor{Light: "#0969da", Dark: "#58a6ff"}
	ColorBorder    = lipgloss.AdaptiveColor{Light: "#d0d7de", Dark: "#30363d"}
	ColorHighlight = lipgloss.AdaptiveColor{Light: "#ddf4ff", Dark: "#1f2c3d"}

	// Status colors.
	StatusPending    = lipgloss.AdaptiveColor{Light: "#9a6700", Dark: "#d29922"}
	StatusInProgress = lipgloss.AdaptiveColor{Light: "#0969da", Dark: "#58a6ff"}
	StatusCompleted  = lipgloss.AdaptiveColor{Light: "#1a7f37", Dark: "#3fb950"}
	StatusFailed     = lipgloss.AdaptiveColor{Light: "#cf222e", Dark: "#f85149"}

	// Tab bar.
	TabActive = lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorAccent).
			PaddingLeft(2).
			PaddingRight(2)

	TabInactive = lipgloss.NewStyle().
			Foreground(ColorMuted).
			PaddingLeft(2).
			PaddingRight(2)

	TabBarLine = lipgloss.NewStyle().
			BorderStyle(lipgloss.NormalBorder()).
			BorderBottom(true).
			BorderForeground(ColorBorder)

	// Stat tiles (dashboard).
	Tile = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorBorder).
		Padding(1, 3).
		Width(18)

	TileLabel = lipgloss.NewStyle().Foreground(ColorMuted)
	TileValue = lipgloss.NewStyle().Bold(true).Foreground(ColorPrimary)

	// Table.
	TableHeader   = lipgloss.NewStyle().Bold(true).Foreground(ColorMuted)
	TableSelected = lipgloss.NewStyle().Background(ColorHighlight).Foreground(ColorPrimary)

	// Forms.
	FormLabel = lipgloss.NewStyle().Foreground(ColorMuted).Width(18)
	FormError = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#cf222e", Dark: "#f85149"}).Italic(true)
	FormHint  = lipgloss.NewStyle().Foreground(ColorMuted).Italic(true)

	// Detail pane.
	DetailKey = lipgloss.NewStyle().Foreground(ColorMuted).Width(16)
	DetailVal = lipgloss.NewStyle().Foreground(ColorPrimary)

	// Section header.
	SectionHeader = lipgloss.NewStyle().Bold(true).Foreground(ColorAccent).PaddingBottom(1)

	// Banners.
	BannerError = lipgloss.NewStyle().
			Background(lipgloss.AdaptiveColor{Light: "#cf222e", Dark: "#f85149"}).
			Foreground(lipgloss.Color("#ffffff")).
			Bold(true).
			Padding(0, 2)

	BannerInfo = lipgloss.NewStyle().
			Background(ColorAccent).
			Foreground(lipgloss.Color("#ffffff")).
			Padding(0, 2)

	// Status bar (bottom).
	StatusBar = lipgloss.NewStyle().
			Foreground(ColorMuted).
			PaddingLeft(1)

	KeyHintKey  = lipgloss.NewStyle().Foreground(ColorAccent).Bold(true)
	KeyHintDesc = lipgloss.NewStyle().Foreground(ColorMuted)

	// Misc.
	Subtle    = lipgloss.NewStyle().Foreground(ColorMuted)
	Bold      = lipgloss.NewStyle().Bold(true)
	Separator = lipgloss.NewStyle().Foreground(ColorBorder).SetString("│")
)

// StatusStyle returns a styled version of the status string.
func StatusStyle(status string) lipgloss.Style {
	switch status {
	case "pending":
		return lipgloss.NewStyle().Foreground(StatusPending)
	case "in_progress":
		return lipgloss.NewStyle().Foreground(StatusInProgress)
	case "completed":
		return lipgloss.NewStyle().Foreground(StatusCompleted)
	case "failed":
		return lipgloss.NewStyle().Foreground(StatusFailed)
	default:
		return lipgloss.NewStyle().Foreground(ColorMuted)
	}
}

// StatusIcon returns a unicode indicator for a job status.
func StatusIcon(status string) string {
	switch status {
	case "pending":
		return "◷"
	case "in_progress":
		return "⟳"
	case "completed":
		return "✓"
	case "failed":
		return "✗"
	default:
		return "·"
	}
}
