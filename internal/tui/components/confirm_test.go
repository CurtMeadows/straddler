package components

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfirm_InvisibleIgnoresKeys(t *testing.T) {
	c := Confirm{}
	cmd := c.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	assert.Nil(t, cmd, "invisible confirm should not emit a command")
}

func TestConfirm_YesEmitsConfirmYesMsg(t *testing.T) {
	c := Confirm{}
	c.Show("Delete?")
	require.True(t, c.Visible)

	cmd := c.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	require.NotNil(t, cmd)

	msg := cmd()
	_, ok := msg.(ConfirmYesMsg)
	assert.True(t, ok)
	assert.False(t, c.Visible, "dialog should hide after confirmation")
}

func TestConfirm_NoEmitsConfirmNoMsg(t *testing.T) {
	c := Confirm{}
	c.Show("Delete?")

	cmd := c.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	require.NotNil(t, cmd)

	msg := cmd()
	_, ok := msg.(ConfirmNoMsg)
	assert.True(t, ok)
	assert.False(t, c.Visible)
}

func TestConfirm_EscEmitsConfirmNoMsg(t *testing.T) {
	c := Confirm{}
	c.Show("Delete?")

	cmd := c.Update(tea.KeyMsg{Type: tea.KeyEsc})
	require.NotNil(t, cmd)

	msg := cmd()
	_, ok := msg.(ConfirmNoMsg)
	assert.True(t, ok)
}

func TestConfirm_EnterEmitsConfirmYesMsg(t *testing.T) {
	c := Confirm{}
	c.Show("Are you sure?")

	cmd := c.Update(tea.KeyMsg{Type: tea.KeyEnter})
	require.NotNil(t, cmd)

	msg := cmd()
	_, ok := msg.(ConfirmYesMsg)
	assert.True(t, ok)
}
