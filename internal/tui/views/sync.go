package views

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/google/go-containerregistry/pkg/name"

	"github.com/CurtMeadows/straddler/internal/config"
	"github.com/CurtMeadows/straddler/internal/db"
	"github.com/CurtMeadows/straddler/internal/registry"
	"github.com/CurtMeadows/straddler/internal/tui/msgs"
	"github.com/CurtMeadows/straddler/internal/tui/styles"
)

// SyncStep identifies the current wizard step.
type SyncStep int

const (
	StepSource SyncStep = iota
	StepDest
	StepOptions
	StepPreview
	StepRunning
	StepDone
)

// SyncModel is a multi-step form for enqueuing sync jobs.
type SyncModel struct {
	cfg       *config.Config
	queue     *db.Queue
	regClient registry.Client

	step          SyncStep
	sourceInput   textinput.Model
	destInput     textinput.Model
	tagFilterInput     textinput.Model
	batchSizeInput textinput.Model
	dryRun        bool
	activeField      int // active input in StepOptions (0=tagFilterInput, 1=batchSize, 2=dryRun)
	validationErr string

	tags   []string
	result *syncResult
	spinner spinner.Model

	width  int
	height int
}

type syncResult struct {
	enqueued int64
	skipped  int64
	dryRun   bool
	tagCount int
}

// NewSync creates a SyncModel.
func NewSync(cfg *config.Config, queue *db.Queue, reg registry.Client) SyncModel {
	src := textinput.New()
	src.Placeholder = "docker.io/library/nginx"
	src.Focus()
	src.CharLimit = 200

	dst := textinput.New()
	dst.Placeholder = "ghcr.io/myorg/nginx"
	dst.CharLimit = 200

	tf := textinput.New()
	tf.Placeholder = "^1\\. (regex, optional)"
	tf.CharLimit = 200

	bs := textinput.New()
	bs.Placeholder = "100"
	bs.SetValue("100")
	bs.CharLimit = 10

	sp := spinner.New()
	sp.Spinner = spinner.Dot

	return SyncModel{
		cfg:            cfg,
		queue:          queue,
		regClient:      reg,
		step:           StepSource,
		sourceInput:    src,
		destInput:      dst,
		tagFilterInput:      tf,
		batchSizeInput: bs,
		spinner:        sp,
	}
}

// SetSize updates the available rendering area.
func (m *SyncModel) SetSize(w, h int) {
	m.width = w
	m.height = h
}

// Reset clears the wizard back to the first step.
func (m *SyncModel) Reset() {
	m.step = StepSource
	m.sourceInput.SetValue("")
	m.destInput.SetValue("")
	m.tagFilterInput.SetValue("")
	m.batchSizeInput.SetValue("100")
	m.dryRun = false
	m.activeField = 0
	m.validationErr = ""
	m.tags = nil
	m.result = nil
	m.sourceInput.Focus()
}

// Init returns the textinput blink command.
func (m SyncModel) Init() tea.Cmd {
	return textinput.Blink
}

// Update handles messages for the sync wizard.
func (m SyncModel) Update(msg tea.Msg) (SyncModel, tea.Cmd) {
	switch m.step {
	case StepSource:
		return m.updateSource(msg)
	case StepDest:
		return m.updateDest(msg)
	case StepOptions:
		return m.updateOptions(msg)
	case StepPreview:
		return m.updatePreview(msg)
	case StepRunning:
		return m.updateRunning(msg)
	case StepDone:
		return m.updateDone(msg)
	}
	return m, nil
}

func (m SyncModel) updateSource(msg tea.Msg) (SyncModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "enter":
			if err := validateRepo(m.sourceInput.Value()); err != nil {
				m.validationErr = err.Error()
				return m, nil
			}
			m.validationErr = ""
			m.step = StepDest
			m.sourceInput.Blur()
			m.destInput.Focus()
			return m, textinput.Blink
		default:
			var cmd tea.Cmd
			m.sourceInput, cmd = m.sourceInput.Update(msg)
			return m, cmd
		}
	default:
		var cmd tea.Cmd
		m.sourceInput, cmd = m.sourceInput.Update(msg)
		return m, cmd
	}
}

func (m SyncModel) updateDest(msg tea.Msg) (SyncModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			m.validationErr = ""
			m.step = StepSource
			m.destInput.Blur()
			m.sourceInput.Focus()
			return m, textinput.Blink
		case "enter":
			if err := validateRepo(m.destInput.Value()); err != nil {
				m.validationErr = err.Error()
				return m, nil
			}
			m.validationErr = ""
			m.step = StepOptions
			m.destInput.Blur()
			m.activeField = 0
			m.tagFilterInput.Focus()
			return m, textinput.Blink
		default:
			var cmd tea.Cmd
			m.destInput, cmd = m.destInput.Update(msg)
			return m, cmd
		}
	default:
		var cmd tea.Cmd
		m.destInput, cmd = m.destInput.Update(msg)
		return m, cmd
	}
}

func (m SyncModel) updateOptions(msg tea.Msg) (SyncModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			m.validationErr = ""
			m.step = StepDest
			m.tagFilterInput.Blur()
			m.batchSizeInput.Blur()
			m.destInput.Focus()
			m.activeField = 0
			return m, textinput.Blink
		case "tab", "shift+tab":
			// Cycle through: tagFilterInput (0), batchSize (1), dryRun toggle (2), proceed (3)
			if msg.String() == "tab" {
				m.activeField = (m.activeField + 1) % 4
			} else {
				m.activeField = (m.activeField - 1 + 4) % 4
			}
			m.tagFilterInput.Blur()
			m.batchSizeInput.Blur()
			switch m.activeField {
			case 0:
				m.tagFilterInput.Focus()
			case 1:
				m.batchSizeInput.Focus()
			}
			return m, textinput.Blink
		case "enter":
			if m.activeField == 2 {
				// Toggle dry-run.
				m.dryRun = !m.dryRun
				return m, nil
			}
			if m.activeField == 3 || m.activeField == 0 || m.activeField == 1 {
				// Proceed to preview.
				if err := validateTagFilter(m.tagFilterInput.Value()); err != nil {
					m.validationErr = err.Error()
					return m, nil
				}
				if err := validateBatchSize(m.batchSizeInput.Value()); err != nil {
					m.validationErr = err.Error()
					return m, nil
				}
				m.validationErr = ""
				m.step = StepPreview
				m.tagFilterInput.Blur()
				m.batchSizeInput.Blur()
				return m, fetchTagsCmd(m.regClient, m.sourceInput.Value(), m.tagFilterInput.Value())
			}
		case " ":
			if m.activeField == 2 {
				m.dryRun = !m.dryRun
				return m, nil
			}
		default:
			var cmd tea.Cmd
			switch m.activeField {
			case 0:
				m.tagFilterInput, cmd = m.tagFilterInput.Update(msg)
			case 1:
				m.batchSizeInput, cmd = m.batchSizeInput.Update(msg)
			}
			return m, cmd
		}
	default:
		var cmd tea.Cmd
		switch m.activeField {
		case 0:
			m.tagFilterInput, cmd = m.tagFilterInput.Update(msg)
		case 1:
			m.batchSizeInput, cmd = m.batchSizeInput.Update(msg)
		}
		return m, cmd
	}
	return m, nil
}

func (m SyncModel) updatePreview(msg tea.Msg) (SyncModel, tea.Cmd) {
	switch msg := msg.(type) {
	case msgs.TagsListedMsg:
		if msg.Err != nil {
			m.validationErr = "Failed to list tags: " + msg.Err.Error()
			m.step = StepOptions
			return m, nil
		}
		m.tags = msg.Tags
		m.validationErr = ""
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "b", "esc":
			m.step = StepOptions
			m.tagFilterInput.Focus()
			return m, textinput.Blink
		case "enter":
			if m.tags == nil {
				return m, nil // still loading
			}
			m.step = StepRunning
			return m, tea.Batch(
				enqueueCmd(m.queue, m.sourceInput.Value(), m.destInput.Value(), m.tags, m.cfg.Worker.MaxAttempts, m.batchSize(), m.dryRun),
				m.spinner.Tick,
			)
		}
	}
	return m, nil
}

func (m SyncModel) updateRunning(msg tea.Msg) (SyncModel, tea.Cmd) {
	switch msg := msg.(type) {
	case msgs.SyncEnqueuedMsg:
		m.step = StepDone
		m.result = &syncResult{
			enqueued: msg.Enqueued,
			skipped:  msg.Skipped,
			dryRun:   m.dryRun,
			tagCount: len(m.tags),
		}
		return m, nil
	default:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}
}

func (m SyncModel) updateDone(msg tea.Msg) (SyncModel, tea.Cmd) {
	if km, ok := msg.(tea.KeyMsg); ok {
		switch km.String() {
		case "enter", "r":
			m.Reset()
			return m, textinput.Blink
		case "d":
			return m, func() tea.Msg { return msgs.SwitchViewMsg{View: ViewDashboard} }
		}
	}
	return m, nil
}

// View renders the sync wizard.
func (m SyncModel) View() string {
	var sb strings.Builder

	sb.WriteString(m.renderBreadcrumb())
	sb.WriteString("\n\n")

	switch m.step {
	case StepSource:
		sb.WriteString("  " + styles.FormLabel.Render("Source repository:") + "\n")
		sb.WriteString("  " + m.sourceInput.View() + "\n")
		sb.WriteString("  " + styles.FormHint.Render("e.g. docker.io/library/nginx"))
	case StepDest:
		sb.WriteString("  " + styles.FormLabel.Render("Source:") + "  " + styles.Subtle.Render(m.sourceInput.Value()) + "\n")
		sb.WriteString("  " + styles.FormLabel.Render("Dest repository:") + "\n")
		sb.WriteString("  " + m.destInput.View() + "\n")
		sb.WriteString("  " + styles.FormHint.Render("e.g. ghcr.io/myorg/nginx") + "\n")
		sb.WriteString("\n  " + styles.KeyHintDesc.Render("[Esc] back"))
	case StepOptions:
		sb.WriteString("  " + styles.FormLabel.Render("Source:") + "  " + styles.Subtle.Render(m.sourceInput.Value()) + "\n")
		sb.WriteString("  " + styles.FormLabel.Render("Dest:") + "    " + styles.Subtle.Render(m.destInput.Value()) + "\n\n")
		sb.WriteString(m.renderOption(0, "Tag filter regex:", m.tagFilterInput.View()))
		sb.WriteString(m.renderOption(1, "Batch size:", m.batchSizeInput.View()))
		dryRunVal := "[ ]"
		if m.dryRun {
			dryRunVal = "[x]"
		}
		dryLabel := "Dry run:"
		if m.activeField == 2 {
			dryLabel = styles.TabActive.Render(dryLabel)
		} else {
			dryLabel = styles.FormLabel.Render(dryLabel)
		}
		sb.WriteString("  " + dryLabel + "  " + dryRunVal + "\n")
		sb.WriteString("\n  " + styles.KeyHintDesc.Render("[Tab] next field  [Enter] proceed  [Esc] back"))
	case StepPreview:
		if m.tags == nil {
			sb.WriteString("  Fetching tags from " + m.sourceInput.Value() + "…")
		} else {
			fmt.Fprintf(&sb, "  Found %d tags", len(m.tags))
			if m.dryRun {
				sb.WriteString("  " + styles.FormHint.Render("(dry run — no jobs will be created)"))
			}
			sb.WriteString("\n\n")
			// Show first 10 tags.
			shown := m.tags
			if len(shown) > 10 {
				shown = shown[:10]
			}
			for _, t := range shown {
				fmt.Fprintf(&sb, "  %s:%s  →  %s:%s\n",
					m.sourceInput.Value(), t, m.destInput.Value(), t)
			}
			if len(m.tags) > 10 {
				fmt.Fprintf(&sb, "  … and %d more\n", len(m.tags)-10)
			}
			sb.WriteString("\n  " + styles.KeyHintKey.Render("[Enter]") + " confirm  " +
				styles.KeyHintKey.Render("[b/Esc]") + " back")
		}
	case StepRunning:
		sb.WriteString("  " + m.spinner.View() + " Enqueueing " + fmt.Sprintf("%d", len(m.tags)) + " jobs…")
	case StepDone:
		if m.result != nil {
			sb.WriteString("  ✓ Done!\n\n")
			if m.result.dryRun {
				fmt.Fprintf(&sb, "  Would have enqueued: %d tags (dry run)\n", m.result.tagCount)
			} else {
				fmt.Fprintf(&sb, "  Enqueued: %d  Skipped: %d\n", m.result.enqueued, m.result.skipped)
			}
			sb.WriteString("\n  " + styles.KeyHintKey.Render("[Enter/R]") + " new sync  " +
				styles.KeyHintKey.Render("[D]") + " dashboard")
		}
	}

	if m.validationErr != "" {
		sb.WriteString("\n\n  " + styles.FormError.Render("✗ "+m.validationErr))
	}

	return sb.String()
}

func (m SyncModel) renderBreadcrumb() string {
	steps := []struct {
		label string
		step  SyncStep
	}{
		{"Source", StepSource},
		{"Dest", StepDest},
		{"Options", StepOptions},
		{"Preview", StepPreview},
	}
	var parts []string
	for _, s := range steps {
		if s.step == m.step {
			parts = append(parts, styles.TabActive.Render(s.label))
		} else {
			parts = append(parts, styles.TabInactive.Render(s.label))
		}
	}
	return strings.Join(parts, styles.Subtle.Render(" › "))
}

func (m SyncModel) renderOption(idx int, label, inputView string) string {
	l := label
	if m.activeField == idx {
		l = styles.TabActive.Render(l)
	} else {
		l = styles.FormLabel.Render(l)
	}
	return "  " + l + "\n  " + inputView + "\n\n"
}

func (m SyncModel) batchSize() int {
	n, err := strconv.Atoi(strings.TrimSpace(m.batchSizeInput.Value()))
	if err != nil || n <= 0 {
		return 100
	}
	return n
}

func validateRepo(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return fmt.Errorf("repository cannot be empty")
	}
	if _, err := name.NewRepository(s); err != nil {
		return fmt.Errorf("invalid repository: %w", err)
	}
	return nil
}

func validateTagFilter(s string) error {
	if s == "" {
		return nil
	}
	if _, err := regexp.Compile(s); err != nil {
		return fmt.Errorf("invalid regex: %w", err)
	}
	return nil
}

func validateBatchSize(s string) error {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n <= 0 {
		return fmt.Errorf("batch size must be a positive integer")
	}
	return nil
}

func fetchTagsCmd(reg registry.Client, source, tagFilter string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		tags, err := reg.ListTags(ctx, source)
		if err != nil {
			return msgs.TagsListedMsg{Source: source, Err: err}
		}
		// Apply filter.
		if tagFilter != "" {
			re, err := regexp.Compile(tagFilter)
			if err != nil {
				return msgs.TagsListedMsg{Source: source, Err: err}
			}
			var filtered []string
			for _, t := range tags {
				if re.MatchString(t) {
					filtered = append(filtered, t)
				}
			}
			tags = filtered
		}
		return msgs.TagsListedMsg{Source: source, Tags: tags}
	}
}

func enqueueCmd(q *db.Queue, source, dest string, tags []string, maxAttempts, batchSize int, dryRun bool) tea.Cmd {
	return func() tea.Msg {
		if dryRun {
			return msgs.SyncEnqueuedMsg{Enqueued: int64(len(tags))}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()

		inserted, err := q.BulkEnqueueTags(ctx, source, dest, tags, maxAttempts, batchSize)
		if err != nil {
			return msgs.SyncEnqueuedMsg{Err: err}
		}
		return msgs.SyncEnqueuedMsg{Enqueued: inserted, Skipped: int64(len(tags)) - inserted}
	}
}
