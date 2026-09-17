package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

type fakeBackend struct {
	items   []stack
	stopped []stack
	downed  []stack
}

func (f *fakeBackend) stacks(context.Context) ([]stack, error) { return f.items, nil }
func (f *fakeBackend) stop(_ context.Context, selected stack) error {
	f.stopped = append(f.stopped, selected)
	return nil
}
func (f *fakeBackend) down(_ context.Context, selected stack) error {
	f.downed = append(f.downed, selected)
	return nil
}

func TestNavigationKeys(t *testing.T) {
	backend := &fakeBackend{items: []stack{{Project: "one"}, {Project: "two"}}}
	m := newModel(backend)
	m.loading = false
	m.stacks = backend.items
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m = next.(model)
	if m.cursor != 1 {
		t.Fatalf("cursor = %d, want 1", m.cursor)
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	m = next.(model)
	if m.cursor != 0 {
		t.Fatalf("cursor = %d, want 0", m.cursor)
	}
}

func TestStopRequiresConfirmation(t *testing.T) {
	backend := &fakeBackend{items: []stack{{Project: "one", Running: 1, ContainerIDs: []string{"id"}}}}
	m := newModel(backend)
	m.loading = false
	m.stacks = backend.items
	next, command := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	m = next.(model)
	if command != nil || m.confirm != confirmStop {
		t.Fatal("stop key should only ask for confirmation")
	}
	next, command = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	m = next.(model)
	if command == nil {
		t.Fatal("confirmation should start the stop command")
	}
	if m.runningAction != "Stopping" || m.actionProject != "one" {
		t.Fatalf("action progress = %q for %q", m.runningAction, m.actionProject)
	}
	batch, ok := command().(tea.BatchMsg)
	if !ok {
		t.Fatal("stop command did not return an action and spinner batch")
	}
	var finished actionFinishedMsg
	for _, batchedCommand := range batch {
		message := batchedCommand()
		if result, ok := message.(actionFinishedMsg); ok {
			finished = result
		}
	}
	if finished.name != "Stop" || finished.project != "one" || finished.affected != 1 {
		t.Fatalf("stop result = %#v", finished)
	}
	if len(backend.stopped) != 1 {
		t.Fatalf("stop calls = %d, want 1", len(backend.stopped))
	}
}

func TestActionProgressAndCompletionFeedback(t *testing.T) {
	m := newModel(&fakeBackend{})
	m.loading = false
	m.width = 100
	m.height = 30
	m.stacks = []stack{{Project: "alpha", Running: 3, Total: 3}}
	m.runningAction = "Stopping"
	m.actionProject = "alpha"
	m.actionCount = 3

	view := m.View()
	for _, expected := range []string{"Stopping alpha (3 containers)…", "Working… please wait"} {
		if !strings.Contains(view, expected) {
			t.Fatalf("progress view missing %q:\n%s", expected, view)
		}
	}

	next, command := m.Update(actionFinishedMsg{name: "Stop", project: "alpha", affected: 3})
	m = next.(model)
	if m.runningAction != "" {
		t.Fatalf("running action was not cleared: %q", m.runningAction)
	}
	if m.status != "✓ Stopped alpha (3 containers)" {
		t.Fatalf("completion status = %q", m.status)
	}
	if command == nil {
		t.Fatal("completion should refresh stacks and clear its status later")
	}
}

func TestActionProgressBlocksNavigation(t *testing.T) {
	m := newModel(&fakeBackend{})
	m.stacks = []stack{{Project: "one"}, {Project: "two"}}
	m.runningAction = "Taking down"
	next, command := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m = next.(model)
	if m.cursor != 0 || command != nil {
		t.Fatalf("input changed model while action was running: cursor=%d", m.cursor)
	}
}

func TestActionFailureFeedback(t *testing.T) {
	m := newModel(&fakeBackend{})
	m.runningAction = "Taking down"
	m.actionProject = "alpha"
	next, command := m.Update(actionFinishedMsg{
		name:    "Down",
		project: "alpha",
		err:     errors.New("Docker daemon unavailable"),
	})
	m = next.(model)
	if m.runningAction != "" || m.loading {
		t.Fatalf("failed action remained busy: action=%q loading=%v", m.runningAction, m.loading)
	}
	want := "✗ Down failed for alpha: Docker daemon unavailable"
	if m.status != want {
		t.Fatalf("failure status = %q, want %q", m.status, want)
	}
	if command == nil {
		t.Fatal("failure status should be cleared later")
	}
}

func TestViewShowsSingleTableAndHotkeys(t *testing.T) {
	m := newModel(&fakeBackend{})
	m.loading = false
	m.width = 120
	m.height = 30
	m.stacks = []stack{{Project: "alpha", Running: 2, Total: 3, Branch: "feature/tui", Path: "/src/alpha"}}
	view := m.View()
	for _, expected := range []string{"PROJECT", "RUNNING", "BRANCH", "PATH", "alpha", "2/3", "feature/tui", "Selected stack", "Containers:", "lazydocker", "ctop"} {
		if !strings.Contains(view, expected) {
			t.Errorf("view missing %q:\n%s", expected, view)
		}
	}
}

func TestPreviewTracksSelectedRow(t *testing.T) {
	m := newModel(&fakeBackend{})
	m.loading = false
	m.width = 120
	m.height = 30
	m.cursor = 1
	m.stacks = []stack{
		{Project: "first", Branch: "feature/first-branch-name-that-is-truncated"},
		{Project: "second", Branch: "feature/second-branch-name-that-is-complete"},
	}
	view := m.View()
	if !strings.Contains(view, "feature/second-branch-name-that-is-complete") {
		t.Fatalf("view does not contain the selected row's full branch:\n%s", view)
	}
	if strings.Contains(view, "feature/first-branch-name-that-is-truncated") {
		t.Fatalf("view contains the unselected row's full truncated branch:\n%s", view)
	}
}

func TestWrapTextPreservesCompleteValue(t *testing.T) {
	value := "/a/very/long/path/to/a/compose/worktree"
	if got := strings.Join(wrapText(value, 9), ""); got != value {
		t.Fatalf("wrapped value = %q, want %q", got, value)
	}
	for _, line := range wrapText(value, 9) {
		if len([]rune(line)) > 9 {
			t.Fatalf("wrapped line %q exceeds its width", line)
		}
	}
}

func TestTruncateText(t *testing.T) {
	if got := truncateText("abcdef", 4); got != "abc…" {
		t.Fatalf("truncateText() = %q", got)
	}
}
