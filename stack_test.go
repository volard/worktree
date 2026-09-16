package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type fakeRunner struct {
	responses map[string][]byte
	errors    map[string]error
	calls     []string
}

func (f *fakeRunner) output(_ context.Context, name string, args ...string) ([]byte, error) {
	key := name
	for _, arg := range args {
		key += " " + arg
	}
	f.calls = append(f.calls, key)
	return f.responses[key], f.errors[key]
}

func TestIsActive(t *testing.T) {
	for _, status := range []string{"running", "restarting", "paused"} {
		if !isActive(status) {
			t.Fatalf("expected %q to be active", status)
		}
	}
	for _, status := range []string{"created", "exited", "dead"} {
		if isActive(status) {
			t.Fatalf("expected %q not to be active", status)
		}
	}
}

func TestBranchForPath(t *testing.T) {
	path := t.TempDir()
	runner := &fakeRunner{responses: map[string][]byte{
		"git -C " + path + " symbolic-ref --quiet --short HEAD": []byte("feature/tui\n"),
	}, errors: map[string]error{}}
	if got := branchForPath(context.Background(), runner, path); got != "feature/tui" {
		t.Fatalf("branchForPath() = %q", got)
	}
}

func TestBranchForMissingPath(t *testing.T) {
	runner := &fakeRunner{responses: map[string][]byte{}, errors: map[string]error{}}
	if got := branchForPath(context.Background(), runner, filepath.Join(t.TempDir(), "gone")); got != "<missing>" {
		t.Fatalf("branchForPath() = %q", got)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("expected no commands, got %v", runner.calls)
	}
}

func TestDockerBackendGroupsOnlyRunningStacks(t *testing.T) {
	path := t.TempDir()
	containers := []inspectedContainer{
		container("one", "alpha", path, "running"),
		container("two", "alpha", path, "exited"),
		container("three", "stopped", "/tmp/stopped", "exited"),
	}
	inspection, err := json.Marshal(containers)
	if err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{responses: map[string][]byte{
		"docker ps --all --quiet --filter label=" + projectLabel: []byte("one\ntwo\nthree\n"),
		"docker inspect one two three":                           inspection,
		"git -C " + path + " symbolic-ref --quiet --short HEAD":  []byte("main\n"),
	}, errors: map[string]error{}}
	got, err := (dockerBackend{runner: runner}).stacks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []stack{{Project: "alpha", Path: path, Branch: "main", Running: 1, Total: 2, ContainerIDs: []string{"one"}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("stacks() = %#v, want %#v", got, want)
	}
}

func TestStopTargetsOnlySelectedContainerIDs(t *testing.T) {
	runner := &fakeRunner{responses: map[string][]byte{"docker stop a b": []byte("a\nb\n")}, errors: map[string]error{}}
	err := (dockerBackend{runner: runner}).stop(context.Background(), stack{Project: "alpha", ContainerIDs: []string{"a", "b"}})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"docker stop a b"}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("calls = %#v, want %#v", runner.calls, want)
	}
}

func TestRunnerErrorIsReturned(t *testing.T) {
	runner := &fakeRunner{responses: map[string][]byte{}, errors: map[string]error{"docker stop a": errors.New("boom")}}
	if err := (dockerBackend{runner: runner}).stop(context.Background(), stack{Project: "alpha", ContainerIDs: []string{"a"}}); err == nil {
		t.Fatal("expected an error")
	}
}

func container(id, project, path, status string) inspectedContainer {
	var result inspectedContainer
	result.ID = id
	result.Config.Labels = map[string]string{projectLabel: project, workdirLabel: path}
	result.State.Status = status
	return result
}

func TestMain(m *testing.M) {
	// Ensure ANSI styling never leaks into string assertions in this package.
	_ = os.Setenv("NO_COLOR", "1")
	os.Exit(m.Run())
}
