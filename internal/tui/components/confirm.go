package components

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/CurtMeadows/straddler/internal/tui/styles"
)

// ConfirmYesMsg is emitted when the user confirms with y/Enter.
type ConfirmYesMsg struct{}

// ConfirmNoMsg is emitted when the user cancels with n/Esc.
type ConfirmNoMsg struct{}

var confirmStyle = lipgloss.NewStyle().
	Border(lipgloss.RoundedBorder()).
	BorderForeground(styles.ColorBorder).
	Padding(1, 3)

// Confirm is a yes/no dialog overlay.
type Confirm struct {
	Question string
	Visible  bool
}

// Show displays the confirmation dialog with the given question.
func (c *Confirm) Show(q string) {
	c.Question = q
	c.Visible = true
}

// Hide dismisses the dialog.
func (c *Confirm) Hide() {
	c.Visible = false
}

// Update handles y/n/Enter/Esc when the dialog is visible.
// Returns a Cmd that emits ConfirmYesMsg or ConfirmNoMsg.
func (c *Confirm) Update(msg tea.Msg) tea.Cmd {
	if !c.Visible {
		return nil
	}
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	switch km.String() {
	case "y", "Y", "enter":
		c.Hide()
		return func() tea.Msg { return ConfirmYesMsg{} }
	case "n", "N", "esc":
		c.Hide()
		return func() tea.Msg { return ConfirmNoMsg{} }
	}
	return nil
}

// View renders the confirm dialog centered within the given width.
func (c *Confirm) View(width int) string {
	if !c.Visible {
		return ""
	}
	content := styles.Bold.Render(c.Question) + "\n\n" +
		styles.KeyHintKey.Render("[y]") + " " + styles.KeyHintDesc.Render("confirm") +
		"   " +
		styles.KeyHintKey.Render("[n/Esc]") + " " + styles.KeyHintDesc.Render("cancel")
	box := confirmStyle.Render(content)
	return lipgloss.PlaceHorizontal(width, lipgloss.Center, box)
}
