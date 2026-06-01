// Package components provides reusable TUI sub-components.
package components

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/CurtMeadows/straddler/internal/tui/msgs"
	"github.com/CurtMeadows/straddler/internal/tui/styles"
)

// Banner is a dismissable message bar embedded in views.
type Banner struct {
	text    string
	isError bool
	Visible bool
}

// Set shows a message.
func (b *Banner) Set(text string, isError bool) {
	b.text = text
	b.isError = isError
	b.Visible = true
}

// Clear hides the banner.
func (b *Banner) Clear() {
	b.Visible = false
	b.text = ""
}

// Update handles ClearBannerMsg.
func (b *Banner) Update(msg tea.Msg) tea.Cmd {
	if _, ok := msg.(msgs.ClearBannerMsg); ok {
		b.Clear()
	}
	return nil
}

// View renders the banner, padded to width.
func (b *Banner) View(width int) string {
	if !b.Visible {
		return ""
	}
	s := b.text
	if b.isError {
		return styles.BannerError.Width(width).Render(s)
	}
	return styles.BannerInfo.Width(width).Render(s)
}
