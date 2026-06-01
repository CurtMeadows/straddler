package components

import (
	"strings"

	"github.com/CurtMeadows/straddler/internal/tui/styles"
)

// KeyHint is a single keybind + description pair shown in the status bar.
type KeyHint struct {
	Key  string
	Desc string
}

// StatusBar renders a one-line keybind hint bar at the bottom of the screen.
type StatusBar struct {
	Hints []KeyHint
	Width int
}

// View renders the status bar.
func (s StatusBar) View() string {
	var parts []string
	for _, h := range s.Hints {
		parts = append(parts,
			styles.KeyHintKey.Render("["+h.Key+"]")+" "+styles.KeyHintDesc.Render(h.Desc),
		)
	}
	line := strings.Join(parts, "  ")
	return styles.StatusBar.Width(s.Width).Render(line)
}
