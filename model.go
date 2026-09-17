package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

type confirmAction int

const (
	confirmNone confirmAction = iota
	confirmStop
	confirmDown
)

type stacksLoadedMsg struct {
	stacks []stack
	err    error
}

type actionFinishedMsg struct {
	name     string
	project  string
	affected int
	err      error
}

type actionTickMsg struct{}

type clearStatusMsg struct{ at time.Time }

type model struct {
	backend       backend
	stacks        []stack
	cursor        int
	width         int
	height        int
	loading       bool
	confirm       confirmAction
	runningAction string
	actionProject string
	actionCount   int
	spinnerFrame  int
	status        string
	statusAt      time.Time
	err           error
}

var spinnerFrames = [...]string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func newModel(backend backend) model {
	return model{backend: backend, loading: true}
}

func (m model) Init() tea.Cmd { return m.loadStacks() }

func (m model) loadStacks() tea.Cmd {
	return func() tea.Msg {
		stacks, err := m.backend.stacks(context.Background())
		return stacksLoadedMsg{stacks: stacks, err: err}
	}
}

func (m model) selected() (stack, bool) {
	if m.cursor < 0 || m.cursor >= len(m.stacks) {
		return stack{}, false
	}
	return m.stacks[m.cursor], true
}

func (m model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case stacksLoadedMsg:
		selectedKey := ""
		if selected, ok := m.selected(); ok {
			selectedKey = selected.key()
		}
		m.loading = false
		m.err = msg.err
		if msg.err == nil {
			m.stacks = msg.stacks
			m.cursor = 0
			for i := range m.stacks {
				if m.stacks[i].key() == selectedKey {
					m.cursor = i
					break
				}
			}
		}
		return m, nil
	case actionFinishedMsg:
		m.confirm = confirmNone
		m.runningAction = ""
		m.actionProject = ""
		m.actionCount = 0
		m.spinnerFrame = 0
		m.loading = true
		if msg.err != nil {
			if msg.project == "" {
				m.setStatus(fmt.Sprintf("✗ %s failed: %v", msg.name, msg.err))
			} else {
				m.setStatus(fmt.Sprintf("✗ %s failed for %s: %v", msg.name, msg.project, msg.err))
			}
			m.loading = false
			return m, m.clearStatusLater()
		}
		switch msg.name {
		case "Stop":
			m.setStatus(fmt.Sprintf("✓ Stopped %s (%d container%s)", msg.project, msg.affected, plural(msg.affected)))
		case "Down":
			m.setStatus(fmt.Sprintf("✓ Compose down finished for %s", msg.project))
		default:
			m.setStatus("✓ " + msg.name + " finished")
		}
		return m, tea.Batch(m.loadStacks(), m.clearStatusLater())
	case actionTickMsg:
		if m.runningAction == "" {
			return m, nil
		}
		m.spinnerFrame = (m.spinnerFrame + 1) % len(spinnerFrames)
		return m, actionTick()
	case clearStatusMsg:
		if msg.at.Equal(m.statusAt) {
			m.status = ""
		}
		return m, nil
	case tea.KeyMsg:
		key := msg.String()
		if key == "ctrl+c" {
			return m, tea.Quit
		}
		if m.runningAction != "" {
			return m, nil
		}
		if m.confirm != confirmNone {
			return m.handleConfirmation(key)
		}
		switch key {
		case "q":
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.stacks)-1 {
				m.cursor++
			}
		case "home", "g":
			m.cursor = 0
		case "end", "G":
			if len(m.stacks) > 0 {
				m.cursor = len(m.stacks) - 1
			}
		case "r":
			m.loading, m.err = true, nil
			return m, m.loadStacks()
		case "s":
			if _, ok := m.selected(); ok {
				m.confirm = confirmStop
			}
		case "d":
			if _, ok := m.selected(); ok {
				m.confirm = confirmDown
			}
		case "enter", "o":
			return m.openShell()
		case "l":
			return m.openTool("lazydocker")
		case "c":
			return m.openTool("ctop")
		}
	}
	return m, nil
}

func (m model) handleConfirmation(key string) (tea.Model, tea.Cmd) {
	if key == "n" || key == "esc" {
		m.confirm = confirmNone
		return m, nil
	}
	if key != "y" {
		return m, nil
	}
	selected, ok := m.selected()
	if !ok {
		m.confirm = confirmNone
		return m, nil
	}
	action := m.confirm
	m.confirm = confirmNone
	m.actionProject = selected.Project
	m.actionCount = selected.Running
	m.spinnerFrame = 0
	if action == confirmDown {
		m.runningAction = "Taking down"
	} else {
		m.runningAction = "Stopping"
	}
	actionCommand := func() tea.Msg {
		var err error
		name := "Stop"
		if action == confirmDown {
			name = "Down"
			err = m.backend.down(context.Background(), selected)
		} else {
			err = m.backend.stop(context.Background(), selected)
		}
		return actionFinishedMsg{name: name, project: selected.Project, affected: selected.Running, err: err}
	}
	return m, tea.Batch(actionCommand, actionTick())
}

func actionTick() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(time.Time) tea.Msg { return actionTickMsg{} })
}

func (m model) openShell() (tea.Model, tea.Cmd) {
	selected, ok := m.selected()
	if !ok {
		return m, nil
	}
	if selected.Path == "" {
		m.setStatus("This stack has no working directory")
		return m, m.clearStatusLater()
	}
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	cmd := exec.Command(shell)
	cmd.Dir = selected.Path
	return m, tea.ExecProcess(cmd, externalFinished("Shell"))
}

func (m model) openTool(tool string) (tea.Model, tea.Cmd) {
	selected, ok := m.selected()
	if !ok {
		return m, nil
	}
	if _, err := exec.LookPath(tool); err != nil {
		m.setStatus(tool + " is not installed or is not in PATH")
		return m, m.clearStatusLater()
	}
	var cmd *exec.Cmd
	if tool == "lazydocker" {
		cmd = exec.Command(tool, "--project", selected.Project)
	} else {
		// ctop's filter keeps the selected Compose project's containers in view.
		cmd = exec.Command(tool, "--filter", selected.Project)
	}
	if selected.Path != "" {
		cmd.Dir = selected.Path
	}
	return m, tea.ExecProcess(cmd, externalFinished(tool))
}

func externalFinished(name string) tea.ExecCallback {
	return func(err error) tea.Msg { return actionFinishedMsg{name: name, err: err} }
}

func plural(count int) string {
	if count == 1 {
		return ""
	}
	return "s"
}

func (m *model) setStatus(status string) {
	m.status = status
	m.statusAt = time.Now()
}

func (m model) clearStatusLater() tea.Cmd {
	at := m.statusAt
	return tea.Tick(5*time.Second, func(time.Time) tea.Msg { return clearStatusMsg{at: at} })
}

func (m model) View() string {
	width := m.width
	if width <= 0 {
		width = 100
	}
	var view strings.Builder
	view.WriteString(bold("workdocker"))
	if m.loading && m.runningAction == "" {
		view.WriteString(dim("  refreshing…"))
	} else {
		view.WriteString(dim(fmt.Sprintf("  %d running stack(s)", len(m.stacks))))
	}
	view.WriteString("\n")

	if m.err != nil {
		view.WriteString(errorStyle("Docker error: " + m.err.Error()))
		view.WriteString("\n\n")
		view.WriteString(dim("r refresh   q quit"))
		return view.String()
	}
	if m.runningAction != "" {
		progress := fmt.Sprintf("%s %s %s", spinnerFrames[m.spinnerFrame], m.runningAction, m.actionProject)
		if m.actionCount > 0 {
			progress += fmt.Sprintf(" (%d container%s)", m.actionCount, plural(m.actionCount))
		}
		view.WriteString(warning(truncateText(progress+"…", width)))
		view.WriteString("\n")
	} else if m.status != "" {
		view.WriteString(dim(truncateText(m.status, width)))
		view.WriteString("\n")
	} else {
		view.WriteString("\n")
	}

	header, rows := renderTable(m.stacks, m.cursor, width)
	preview := []string(nil)
	if selected, ok := m.selected(); ok {
		preview = renderStackPreview(selected, width)
	}
	view.WriteString(header)
	view.WriteByte('\n')
	if len(rows) == 0 {
		view.WriteString(dim("No running Compose stacks found."))
		view.WriteByte('\n')
	} else {
		visible := m.height - 7 - len(preview)
		if visible < 1 {
			visible = 1
		}
		start := 0
		if m.cursor >= visible {
			start = m.cursor - visible + 1
		}
		end := min(len(rows), start+visible)
		for _, row := range rows[start:end] {
			view.WriteString(row)
			view.WriteByte('\n')
		}
	}
	if len(preview) > 0 {
		view.WriteByte('\n')
		for _, line := range preview {
			view.WriteString(line)
			view.WriteByte('\n')
		}
	}

	if m.runningAction != "" {
		view.WriteString(dim("Working… please wait"))
	} else if m.confirm != confirmNone {
		selected, _ := m.selected()
		action := "Stop"
		if m.confirm == confirmDown {
			action = "Run compose down for"
		}
		view.WriteString(warning(fmt.Sprintf("%s %s? y/N", action, selected.Project)))
	} else {
		view.WriteString(dim("↑/k ↓/j move  enter/o shell  s stop  d down  l lazydocker  c ctop  r refresh  q quit"))
	}
	return view.String()
}

func renderTable(stacks []stack, cursor, width int) (string, []string) {
	projectWidth, countWidth, branchWidth := 24, 10, 22
	if width < 90 {
		projectWidth, branchWidth = 18, 16
	}
	pathWidth := width - projectWidth - countWidth - branchWidth - 7
	if pathWidth < 12 {
		pathWidth = 12
	}
	format := fmt.Sprintf("%%-%ds  %%-%ds  %%-%ds  %%-%ds", projectWidth, countWidth, branchWidth, pathWidth)
	header := bold(fmt.Sprintf(format, "PROJECT", "RUNNING", "BRANCH", "PATH"))
	rows := make([]string, 0, len(stacks))
	for i, current := range stacks {
		line := fmt.Sprintf(format,
			truncateText(current.Project, projectWidth),
			fmt.Sprintf("%d/%d", current.Running, current.Total),
			truncateText(current.Branch, branchWidth),
			truncateText(current.Path, pathWidth),
		)
		if i == cursor {
			line = selectedStyle(line)
		}
		rows = append(rows, line)
	}
	return header, rows
}

func renderStackPreview(selected stack, width int) []string {
	if width < 1 {
		width = 1
	}
	lines := []string{bold("Selected stack")}
	lines = append(lines, renderPreviewField("Project", selected.Project, width)...)
	lines = append(lines, renderPreviewField("Containers", fmt.Sprintf("%d/%d running", selected.Running, selected.Total), width)...)
	lines = append(lines, renderPreviewField("Branch", selected.Branch, width)...)
	lines = append(lines, renderPreviewField("Path", selected.Path, width)...)
	return lines
}

func renderPreviewField(label, value string, width int) []string {
	if value == "" {
		value = "<unknown>"
	}
	prefix := fmt.Sprintf("  %-11s ", label+":")
	prefixWidth := len([]rune(prefix))
	if width <= prefixWidth+1 {
		lines := []string{strings.TrimRight(prefix, " ")}
		for _, line := range wrapText(value, max(1, width-2)) {
			lines = append(lines, "  "+line)
		}
		return lines
	}

	wrapped := wrapText(value, width-prefixWidth)
	lines := make([]string, 0, len(wrapped))
	indent := strings.Repeat(" ", prefixWidth)
	for i, line := range wrapped {
		if i == 0 {
			lines = append(lines, prefix+line)
		} else {
			lines = append(lines, indent+line)
		}
	}
	return lines
}

func wrapText(value string, width int) []string {
	if width < 1 {
		width = 1
	}
	characters := []rune(value)
	if len(characters) == 0 {
		return []string{""}
	}
	lines := make([]string, 0, (len(characters)+width-1)/width)
	for len(characters) > 0 {
		end := min(width, len(characters))
		lines = append(lines, string(characters[:end]))
		characters = characters[end:]
	}
	return lines
}

func truncateText(value string, width int) string {
	characters := []rune(value)
	if len(characters) <= width {
		return value
	}
	if width <= 1 {
		return string(characters[:width])
	}
	return string(characters[:width-1]) + "…"
}

func noColor() bool { return os.Getenv("NO_COLOR") != "" }

func styled(code, value string) string {
	if noColor() {
		return value
	}
	return "\x1b[" + code + "m" + value + "\x1b[0m"
}

func bold(value string) string          { return styled("1", value) }
func dim(value string) string           { return styled("2", value) }
func selectedStyle(value string) string { return styled("7", value) }
func errorStyle(value string) string    { return styled("31", value) }
func warning(value string) string       { return styled("33;1", value) }
