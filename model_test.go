package main

import (
	"context"
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
	backend := &fakeBackend{items: []stack{{Project: "one", ContainerIDs: []string{"id"}}}}
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
	if _, ok := command().(actionFinishedMsg); !ok {
		t.Fatal("stop command returned an unexpected message")
	}
	if len(backend.stopped) != 1 {
		t.Fatalf("stop calls = %d, want 1", len(backend.stopped))
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
