package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

const (
	projectLabel = "com.docker.compose.project"
	workdirLabel = "com.docker.compose.project.working_dir"
)

type stack struct {
	Project      string
	Path         string
	Branch       string
	Running      int
	Total        int
	ContainerIDs []string
}

func (s stack) key() string { return s.Project + "\x00" + s.Path }

type backend interface {
	stacks(context.Context) ([]stack, error)
	stop(context.Context, stack) error
	down(context.Context, stack) error
}

type commandRunner interface {
	output(context.Context, string, ...string) ([]byte, error)
}

type execRunner struct{}

func (execRunner) output(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.Output()
	if err == nil {
		return out, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		message := strings.TrimSpace(string(exitErr.Stderr))
		if message != "" {
			return nil, fmt.Errorf("%s: %s", name, message)
		}
	}
	return nil, fmt.Errorf("%s: %w", name, err)
}

type dockerBackend struct {
	runner commandRunner
}

func newDockerBackend() dockerBackend { return dockerBackend{runner: execRunner{}} }

type inspectedContainer struct {
	ID     string `json:"Id"`
	Config struct {
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
	State struct {
		Status string `json:"Status"`
	} `json:"State"`
}

func (d dockerBackend) stacks(ctx context.Context) ([]stack, error) {
	if _, err := exec.LookPath("docker"); err != nil {
		return nil, errors.New("Docker CLI is not installed or is not in PATH")
	}

	out, err := d.runner.output(ctx, "docker", "ps", "--all", "--quiet", "--filter", "label="+projectLabel)
	if err != nil {
		return nil, fmt.Errorf("list Compose containers: %w", err)
	}
	ids := strings.Fields(string(out))
	if len(ids) == 0 {
		return nil, nil
	}

	args := append([]string{"inspect"}, ids...)
	out, err = d.runner.output(ctx, "docker", args...)
	if err != nil {
		return nil, fmt.Errorf("inspect Compose containers: %w", err)
	}
	var containers []inspectedContainer
	if err := json.Unmarshal(out, &containers); err != nil {
		return nil, fmt.Errorf("decode Docker inspection: %w", err)
	}

	grouped := make(map[string]*stack)
	for _, container := range containers {
		project := container.Config.Labels[projectLabel]
		if project == "" {
			continue
		}
		path := container.Config.Labels[workdirLabel]
		key := project + "\x00" + path
		current := grouped[key]
		if current == nil {
			current = &stack{Project: project, Path: path}
			grouped[key] = current
		}
		current.Total++
		if isActive(container.State.Status) {
			current.Running++
			current.ContainerIDs = append(current.ContainerIDs, container.ID)
		}
	}

	result := make([]stack, 0, len(grouped))
	for _, current := range grouped {
		if current.Running == 0 {
			continue
		}
		current.Branch = branchForPath(ctx, d.runner, current.Path)
		result = append(result, *current)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Path == result[j].Path {
			return result[i].Project < result[j].Project
		}
		return result[i].Path < result[j].Path
	})
	return result, nil
}

func isActive(status string) bool {
	switch status {
	case "running", "restarting", "paused":
		return true
	default:
		return false
	}
}

func branchForPath(ctx context.Context, runner commandRunner, path string) string {
	if path == "" {
		return "<unknown>"
	}
	if info, err := os.Stat(path); err != nil || !info.IsDir() {
		return "<missing>"
	}
	if out, err := runner.output(ctx, "git", "-C", path, "symbolic-ref", "--quiet", "--short", "HEAD"); err == nil {
		return strings.TrimSpace(string(out))
	}
	if out, err := runner.output(ctx, "git", "-C", path, "rev-parse", "--short", "HEAD"); err == nil {
		return "detached:" + strings.TrimSpace(string(out))
	}
	return "<not a Git worktree>"
}

func (d dockerBackend) stop(ctx context.Context, selected stack) error {
	if len(selected.ContainerIDs) == 0 {
		return errors.New("the selected stack has no running containers")
	}
	args := append([]string{"stop"}, selected.ContainerIDs...)
	if _, err := d.runner.output(ctx, "docker", args...); err != nil {
		return fmt.Errorf("stop %s: %w", selected.Project, err)
	}
	return nil
}

func (d dockerBackend) down(ctx context.Context, selected stack) error {
	if selected.Path == "" {
		return errors.New("the selected stack has no Compose working directory")
	}
	info, err := os.Stat(selected.Path)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("Compose working directory is unavailable: %s", selected.Path)
	}
	path, err := filepath.Abs(selected.Path)
	if err != nil {
		return fmt.Errorf("resolve Compose working directory: %w", err)
	}
	// --project-directory selects the same Compose context without relying on
	// process-wide directory changes.
	_, err = d.runner.output(ctx, "docker", "compose", "--project-directory", path, "--project-name", selected.Project, "down", "--remove-orphans")
	if err != nil {
		return fmt.Errorf("down %s: %w", selected.Project, err)
	}
	return nil
}
