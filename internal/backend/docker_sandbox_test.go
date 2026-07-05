package backend

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liu-qingyuan/safe-claude-sbx/internal/config"
)

func TestDockerSandboxAvailabilityDistinguishesStates(t *testing.T) {
	tests := []struct {
		name     string
		runner   stubRunner
		wantKind AvailabilityKind
		wantOK   bool
	}{
		{
			name: "available",
			runner: stubRunner{
				path: "/tmp/sbx",
				results: map[string]CommandResult{
					"sbx version": {Stdout: "sbx version: v0.34.0 fake\n"},
					"sbx ls":      {Stdout: "No sandboxes found.\n"},
				},
			},
			wantKind: AvailabilityAvailable,
			wantOK:   true,
		},
		{
			name:     "binary unavailable",
			runner:   stubRunner{lookPathErr: errors.New("not found")},
			wantKind: AvailabilityUnavailable,
		},
		{
			name: "version incompatible",
			runner: stubRunner{
				path: "/tmp/sbx",
				results: map[string]CommandResult{
					"sbx version": {Stdout: "unexpected tool\n"},
				},
			},
			wantKind: AvailabilityIncompatible,
		},
		{
			name: "backend unreachable",
			runner: stubRunner{
				path: "/tmp/sbx",
				results: map[string]CommandResult{
					"sbx version": {Stdout: "sbx version: v0.34.0 fake\n"},
					"sbx ls":      {Stderr: "ERROR: Not authenticated to Docker\n"},
				},
				errors: map[string]error{
					"sbx ls": errors.New("exit status 1"),
				},
			},
			wantKind: AvailabilityUnavailable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := (DockerSandbox{Runner: tt.runner, Binary: "sbx"}).CheckAvailability(context.Background())

			if got.Kind != tt.wantKind {
				t.Fatalf("expected kind %q, got %#v with err %v", tt.wantKind, got, err)
			}
			if got.OK != tt.wantOK {
				t.Fatalf("expected OK %v, got %#v", tt.wantOK, got)
			}
			if tt.wantOK && err != nil {
				t.Fatalf("availability unexpectedly failed: %v", err)
			}
			if !tt.wantOK && err == nil {
				t.Fatalf("availability unexpectedly succeeded: %#v", got)
			}
		})
	}
}

func TestDockerSandboxProbeReturnsStructuredInspectionAndIdempotentCleanup(t *testing.T) {
	runner := stubRunner{
		path: "/tmp/sbx",
		results: map[string]CommandResult{
			"sbx create --name probe shell .":                                        {Stdout: "created\n"},
			"sbx exec -e TZ=UTC -e LANG=en_US.UTF-8 -e LC_ALL=en_US.UTF-8 probe env": {Stdout: "PATH=/usr/bin\nHTTP_PROXY=http://gateway.docker.internal:3128\n"},
			"sbx exec probe pwd":                                                     {Stdout: "/workspace\n"},
			"sbx exec probe mount":                                                   {Stdout: "/dev/disk1 on /workspace type virtiofs\n"},
			"sbx exec -e TZ=UTC probe date":                                          {Stdout: "Sun Jul 5 00:00:00 UTC 2026\n"},
			"sbx exec -e LANG=en_US.UTF-8 -e LC_ALL=en_US.UTF-8 probe locale":        {Stdout: "LANG=en_US.UTF-8\n"},
			"sbx exec probe curl -fsS https://example.test/ip":                       {Stdout: "203.0.113.10\n"},
			"sbx stop probe":                                                         {Stderr: "sandbox not found\n"},
			"sbx rm --force probe":                                                   {Stderr: "sandbox not found\n"},
		},
		errors: map[string]error{
			"sbx stop probe":       errors.New("exit status 1"),
			"sbx rm --force probe": errors.New("exit status 1"),
		},
	}

	result, err := (DockerSandbox{Runner: runner, Binary: "sbx"}).Probe(context.Background(), probeConfig())

	if err != nil {
		t.Fatalf("probe failed: %v", err)
	}
	if !result.CleanupDone {
		t.Fatalf("expected cleanup to be marked done")
	}
	if result.Egress.ObservedIP != "203.0.113.10" {
		t.Fatalf("expected observed egress IP, got %#v", result.Egress)
	}
	if result.Inspection.Environment["HTTP_PROXY"] != "http://gateway.docker.internal:3128" {
		t.Fatalf("expected structured env observation, got %#v", result.Inspection.Environment)
	}
	if result.Inspection.WorkingDirectory != "/workspace" || !strings.Contains(result.Inspection.Mounts, "/workspace") {
		t.Fatalf("expected pwd and mount observations, got %#v", result.Inspection)
	}

	if err := (DockerSandbox{Runner: runner, Binary: "sbx"}).CleanupProbe(context.Background(), probeConfig()); err != nil {
		t.Fatalf("cleanup should be idempotent for already missing probe: %v", err)
	}
}

func TestDockerSandboxStartMainPassesMainSandboxContract(t *testing.T) {
	runner := stubRunner{
		path: "/tmp/sbx",
		results: map[string]CommandResult{
			"sbx run claude --name main-sbx /work/project": {Stdout: "started\n"},
		},
	}
	cfg := probeConfig()
	cfg.Sandbox.MainName = "main-sbx"
	cfg.Workspace.Mount = "/work/project"
	plan := NewStartPlan(cfg)

	result, err := (DockerSandbox{Runner: runner, Binary: "sbx"}).StartMain(context.Background(), plan)

	if err != nil {
		t.Fatalf("start main failed: %v", err)
	}
	if result.SandboxName != "main-sbx" || result.Agent != "claude" || result.Workspace != "/work/project" || result.Timezone != "UTC" || result.Locale != "en_US.UTF-8" {
		t.Fatalf("unexpected start result: %#v", result)
	}
	if plan.Environment["TZ"] != "UTC" || plan.Environment["LANG"] != "en_US.UTF-8" || plan.Environment["LC_ALL"] != "en_US.UTF-8" {
		t.Fatalf("expected allowed startup environment, got %#v", plan.Environment)
	}
}

func TestDockerSandboxStartMainAttachedConnectsStdin(t *testing.T) {
	dir := t.TempDir()
	stdinPath := filepath.Join(dir, "stdin.txt")
	fakeSBX := filepath.Join(dir, "sbx")
	script := fmt.Sprintf(`#!/bin/sh
set -eu
if [ "$1" != "run" ]; then
  printf 'unexpected command: %%s\n' "$*" >&2
  exit 1
fi
IFS= read -r line
printf '%%s' "$line" > %q
printf 'main sandbox started\n'
`, stdinPath)
	if err := os.WriteFile(fakeSBX, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake sbx: %v", err)
	}

	cfg := probeConfig()
	plan := NewStartPlan(cfg)
	_, wait, err := (DockerSandbox{Binary: fakeSBX}).StartMainAttached(context.Background(), plan, strings.NewReader("terminal-input\n"), io.Discard, io.Discard)
	if err != nil {
		t.Fatalf("start attached failed: %v", err)
	}
	if wait == nil {
		t.Fatal("expected wait channel")
	}
	if err := <-wait; err != nil {
		t.Fatalf("attached command failed: %v", err)
	}
	got, err := os.ReadFile(stdinPath)
	if err != nil {
		t.Fatalf("read captured stdin: %v", err)
	}
	if string(got) != "terminal-input" {
		t.Fatalf("expected stdin to be connected to sbx run, got %q", got)
	}
}

func TestDockerSandboxProbeFailsClosedForUnsafeInspection(t *testing.T) {
	tests := []struct {
		name      string
		env       string
		mounts    string
		wantError string
	}{
		{
			name:      "host proxy",
			env:       "PATH=/usr/bin\nHTTP_PROXY=http://127.0.0.1:7897\n",
			mounts:    "/dev/disk1 on /workspace type virtiofs (rw,source=/Users/alice/work/safe-claude-sbx)\n",
			wantError: "environment.inspection.env.HTTP_PROXY",
		},
		{
			name:      "sensitive env",
			env:       "PATH=/usr/bin\nSSH_AUTH_SOCK=/Users/alice/.ssh/agent.sock\n",
			mounts:    "/dev/disk1 on /workspace type virtiofs (rw,source=/Users/alice/work/safe-claude-sbx)\n",
			wantError: "environment.inspection.env.SSH_AUTH_SOCK",
		},
		{
			name:      "sensitive mount",
			env:       "PATH=/usr/bin\nHTTP_PROXY=http://gateway.docker.internal:3128\n",
			mounts:    "/dev/disk1 on /workspace type virtiofs\n/dev/disk2 on /host-ssh type virtiofs (rw,source=/Users/alice/.ssh)\n",
			wantError: "workspace.inspection.mounts",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := probeRunner(tt.env, tt.mounts)
			cfg := probeConfig()
			cfg.Workspace.ForbiddenPaths = append(cfg.Workspace.ForbiddenPaths, "/Users/alice/.ssh")

			_, err := (DockerSandbox{Runner: runner, Binary: "sbx"}).Probe(context.Background(), cfg)

			if err == nil {
				t.Fatalf("probe unexpectedly succeeded")
			}
			if !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("expected %q in error, got %v", tt.wantError, err)
			}
			if strings.Contains(err.Error(), "127.0.0.1:7897") || strings.Contains(err.Error(), "/Users/alice/.ssh/agent.sock") {
				t.Fatalf("probe error leaked sensitive value: %v", err)
			}
		})
	}
}

func TestDockerSandboxProbeCleansUpWithIndependentContextAfterTimeout(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	runner := timeoutCleanupRunner{
		results: map[string]CommandResult{
			"sbx create --name probe shell .": {Stdout: "created\n"},
			"sbx stop probe":                  {Stderr: "sandbox not found\n"},
			"sbx rm --force probe":            {Stderr: "sandbox not found\n"},
		},
		cancel: cancel,
	}

	result, err := (DockerSandbox{Runner: &runner, Binary: "sbx"}).Probe(ctx, probeConfig())

	if err == nil {
		t.Fatalf("probe unexpectedly succeeded")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected timeout error, got %v", err)
	}
	if !result.CleanupDone {
		t.Fatalf("expected cleanup to be marked done after timeout")
	}
	if runner.cleanupUsedCanceledContext {
		t.Fatalf("cleanup reused the canceled probe context")
	}
	if !runner.sawStop || !runner.sawRemove {
		t.Fatalf("expected stop and rm cleanup after timeout, got stop=%v rm=%v", runner.sawStop, runner.sawRemove)
	}
}

func probeConfig() config.Config {
	return config.Config{
		Network: config.Network{
			EgressIP: config.EgressIP{
				ExpectedIP:      "203.0.113.10",
				SandboxCheckURL: "https://example.test/ip",
				TimeoutSeconds:  10,
			},
		},
		Sandbox: config.Sandbox{
			Backend:   "docker-sandbox",
			ProbeName: "probe",
			Agent:     "claude",
		},
		Workspace: config.Workspace{
			Mount:          ".",
			ForbiddenPaths: []string{"~", "~/.ssh", "~/.claude", "~/.config/clash", "~/Library/Keychains"},
		},
		Environment: config.Environment{
			Timezone:         "UTC",
			Locale:           "en_US.UTF-8",
			ForbiddenEnvVars: []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY"},
		},
		Cleanup: config.Cleanup{
			RemoveProbeSandbox: true,
		},
	}
}

func probeRunner(env, mounts string) stubRunner {
	return stubRunner{
		path: "/tmp/sbx",
		results: map[string]CommandResult{
			"sbx create --name probe shell .":                                        {Stdout: "created\n"},
			"sbx exec -e TZ=UTC -e LANG=en_US.UTF-8 -e LC_ALL=en_US.UTF-8 probe env": {Stdout: env},
			"sbx exec probe pwd":                                                     {Stdout: "/workspace\n"},
			"sbx exec probe mount":                                                   {Stdout: mounts},
			"sbx exec -e TZ=UTC probe date":                                          {Stdout: "Sun Jul 5 00:00:00 UTC 2026\n"},
			"sbx exec -e LANG=en_US.UTF-8 -e LC_ALL=en_US.UTF-8 probe locale":        {Stdout: "LANG=en_US.UTF-8\n"},
			"sbx exec probe curl -fsS https://example.test/ip":                       {Stdout: "203.0.113.10\n"},
			"sbx stop probe":                                                         {Stderr: "sandbox not found\n"},
			"sbx rm --force probe":                                                   {Stderr: "sandbox not found\n"},
		},
		errors: map[string]error{
			"sbx stop probe":       errors.New("exit status 1"),
			"sbx rm --force probe": errors.New("exit status 1"),
		},
	}
}

type timeoutCleanupRunner struct {
	results                    map[string]CommandResult
	cancel                     context.CancelFunc
	sawStop                    bool
	sawRemove                  bool
	cleanupUsedCanceledContext bool
}

func (r *timeoutCleanupRunner) LookPath(file string) (string, error) {
	return file, nil
}

func (r *timeoutCleanupRunner) Run(ctx context.Context, name string, args ...string) (CommandResult, error) {
	key := strings.Join(append([]string{name}, args...), " ")
	switch key {
	case "sbx exec -e TZ=UTC -e LANG=en_US.UTF-8 -e LC_ALL=en_US.UTF-8 probe env":
		r.cancel()
		return CommandResult{}, context.DeadlineExceeded
	case "sbx stop probe":
		r.sawStop = true
		if ctx.Err() != nil {
			r.cleanupUsedCanceledContext = true
			return CommandResult{}, ctx.Err()
		}
		return r.results[key], errors.New("exit status 1")
	case "sbx rm --force probe":
		r.sawRemove = true
		if ctx.Err() != nil {
			r.cleanupUsedCanceledContext = true
			return CommandResult{}, ctx.Err()
		}
		return r.results[key], errors.New("exit status 1")
	default:
		result, ok := r.results[key]
		if !ok {
			return CommandResult{}, fmt.Errorf("unexpected command %q", key)
		}
		return result, nil
	}
}

type stubRunner struct {
	path        string
	lookPathErr error
	results     map[string]CommandResult
	errors      map[string]error
}

func (r stubRunner) LookPath(file string) (string, error) {
	if r.lookPathErr != nil {
		return "", r.lookPathErr
	}
	if r.path != "" {
		return r.path, nil
	}
	return file, nil
}

func (r stubRunner) Run(ctx context.Context, name string, args ...string) (CommandResult, error) {
	key := strings.Join(append([]string{name}, args...), " ")
	result := r.results[key]
	return result, r.errors[key]
}
