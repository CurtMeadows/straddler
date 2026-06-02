package views

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CurtMeadows/straddler/internal/tui/msgs"
)

func pressEnter(m SyncModel) SyncModel {
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	return m
}

func pressEsc(m SyncModel) SyncModel {
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	return m
}

func TestSyncWizard_AdvancesOnValidInput(t *testing.T) {
	m := NewSync(nil, nil, nil)
	assert.Equal(t, StepSource, m.step)

	// Type a valid source repo and press Enter.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("docker.io/library/nginx")})
	m = pressEnter(m)
	assert.Equal(t, StepDest, m.step, "valid source should advance to StepDest")
	assert.Empty(t, m.validationErr)
}

func TestSyncWizard_RejectsEmptySource(t *testing.T) {
	m := NewSync(nil, nil, nil)
	m = pressEnter(m) // press Enter without typing anything
	assert.Equal(t, StepSource, m.step, "empty source should stay on StepSource")
	assert.NotEmpty(t, m.validationErr)
}

func TestSyncWizard_EscGoesBack(t *testing.T) {
	m := NewSync(nil, nil, nil)

	// Advance to StepDest.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("docker.io/library/nginx")})
	m = pressEnter(m)
	require.Equal(t, StepDest, m.step)

	// Esc should return to StepSource.
	m = pressEsc(m)
	assert.Equal(t, StepSource, m.step)
}

func TestSyncWizard_RejectsInvalidTagFilterRegex(t *testing.T) {
	m := NewSync(nil, nil, nil)

	// Advance to StepDest.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("docker.io/library/nginx")})
	m = pressEnter(m)
	// Advance to StepOptions.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("ghcr.io/myorg/nginx")})
	m = pressEnter(m)
	require.Equal(t, StepOptions, m.step)

	// Type an invalid regex into the tag filter.
	m.tagFilterInput.SetValue("[invalid")
	m = pressEnter(m) // try to advance
	assert.Equal(t, StepOptions, m.step, "invalid regex should stay on StepOptions")
	assert.NotEmpty(t, m.validationErr)
}

func TestSyncWizard_DryRunToggle(t *testing.T) {
	m := NewSync(nil, nil, nil)

	// Advance to StepOptions.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("docker.io/library/nginx")})
	m = pressEnter(m)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("ghcr.io/myorg/nginx")})
	m = pressEnter(m)
	require.Equal(t, StepOptions, m.step)

	// Tab twice to reach the dry-run field (index 2).
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	assert.Equal(t, 2, m.activeField)
	assert.False(t, m.dryRun)

	// Space or Enter toggles dry-run on.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(" ")})
	assert.True(t, m.dryRun)

	// Toggle it back off.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(" ")})
	assert.False(t, m.dryRun)
}

func TestSyncWizard_DoneNavigatesToDashboard(t *testing.T) {
	m := NewSync(nil, nil, nil)
	m.step = StepDone
	m.result = &syncResult{enqueued: 5}

	var switchMsg msgs.SwitchViewMsg
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	_ = m
	require.NotNil(t, cmd)

	msg := cmd()
	switchMsg, ok := msg.(msgs.SwitchViewMsg)
	require.True(t, ok, "pressing d on StepDone should emit SwitchViewMsg")
	assert.Equal(t, ViewDashboard, switchMsg.View)
}

func TestSyncWizard_Reset(t *testing.T) {
	m := NewSync(nil, nil, nil)
	m.step = StepDone
	m.validationErr = "some error"
	m.dryRun = true
	m.activeField = 3

	m.Reset()

	assert.Equal(t, StepSource, m.step)
	assert.Empty(t, m.validationErr)
	assert.False(t, m.dryRun)
	assert.Equal(t, 0, m.activeField)
	assert.Nil(t, m.tags)
	assert.Nil(t, m.result)
}
