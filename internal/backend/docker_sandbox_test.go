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
	"time"

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
	calls := []string{}
	runner := stubRunner{
		path:  "/tmp/sbx",
		calls: &calls,
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
	if got := strings.Join(calls, "\n"); got != "sbx run claude --name main-sbx /work/project" {
		t.Fatalf("direct mode should not inspect or rebuild main sandbox, got:\n%s", got)
	}
}

func TestSBXProcessEnvDropsHostHerdrState(t *testing.T) {
	t.Setenv("HERDR_ENV", "1")
	t.Setenv("HERDR_SOCKET_PATH", "/tmp/host-herdr.sock")
	t.Setenv("HERDR_PANE_ID", "host-pane")
	t.Setenv("HERDR_WORKSPACE_ID", "host-workspace")

	for _, entry := range sbxProcessEnv() {
		if strings.HasPrefix(entry, "HERDR_") {
			t.Fatalf("sbx subprocess environment inherited host Herdr state: %s", entry)
		}
	}
}

func TestDockerSandboxStartMainPreparesSandboxLocalHerdr(t *testing.T) {
	calls := []string{}
	runner := stubRunner{
		path:  "/tmp/sbx",
		calls: &calls,
		results: map[string]CommandResult{
			"sbx create --name main-sbx claude /work/project":    {Stdout: "created\n"},
			"sbx exec main-sbx command -v herdr":                 {Stdout: "/home/agent/.local/bin/herdr\n"},
			"sbx exec main-sbx herdr --version":                  {Stdout: "herdr 0.7.1\n"},
			"sbx exec main-sbx herdr integration install claude": {Stdout: "installed\n"},
			"sbx exec main-sbx herdr server":                     {Stdout: "server started\n"},
			"sbx exec main-sbx herdr status server --json":       {Stdout: `{"running":true,"socket":"/home/agent/.config/herdr/herdr.sock"}` + "\n"},
			"sbx exec -e HERDR_ENV=1 -e HERDR_SOCKET_PATH=/home/agent/.config/herdr/herdr.sock -e HERDR_PANE_ID=sandbox-claude main-sbx claude": {Stdout: "claude started\n"},
		},
	}
	cfg := probeConfig()
	cfg.Sandbox.MainName = "main-sbx"
	cfg.Workspace.Mount = "/work/project"
	installIfMissing := true
	cfg.Sandbox.Supervision = config.Supervision{
		Mode: "sandbox-local-herdr",
		Herdr: &config.HerdrSupervision{
			InstallIfMissing: &installIfMissing,
			SocketPath:       "/home/agent/.config/herdr/herdr.sock",
			PaneID:           "sandbox-claude",
		},
	}
	plan := NewStartPlan(cfg)

	result, err := (DockerSandbox{Runner: runner, Binary: "sbx"}).StartMain(context.Background(), plan)

	if err != nil {
		t.Fatalf("start main failed: %v", err)
	}
	if result.SandboxName != "main-sbx" || result.Agent != "claude" || result.Workspace != "/work/project" {
		t.Fatalf("unexpected start result: %#v", result)
	}
	for _, key := range runner.keys() {
		if strings.Contains(key, "/tmp/host-herdr.sock") || strings.Contains(key, "host-pane") {
			t.Fatalf("start command used host Herdr state: %s", key)
		}
	}
	got := strings.Join(calls, "\n")
	want := strings.Join([]string{
		"sbx ls",
		"sbx create --name main-sbx claude /work/project",
		"sbx exec main-sbx command -v herdr",
		"sbx exec main-sbx herdr --version",
		"sbx exec main-sbx herdr integration install claude",
		"sbx exec main-sbx herdr server",
		"sbx exec main-sbx herdr status server --json",
		"sbx exec -e HERDR_ENV=1 -e HERDR_SOCKET_PATH=/home/agent/.config/herdr/herdr.sock -e HERDR_PANE_ID=sandbox-claude main-sbx claude",
	}, "\n")
	if !strings.Contains(got, want) {
		t.Fatalf("expected first Herdr startup to inspect then create main sandbox, got:\n%s", got)
	}
}

func TestDockerSandboxStartMainWaitsForSandboxLocalHerdrReadiness(t *testing.T) {
	calls := []string{}
	runner := sequenceRunner{
		stubRunner: stubRunner{
			path:  "/tmp/sbx",
			calls: &calls,
			results: map[string]CommandResult{
				"sbx ls": {Stdout: "No sandboxes found.\n"},
				"sbx create --name main-sbx claude /work/project":    {Stdout: "created\n"},
				"sbx exec main-sbx command -v herdr":                 {Stdout: "/home/agent/.local/bin/herdr\n"},
				"sbx exec main-sbx herdr --version":                  {Stdout: "herdr 0.7.1\n"},
				"sbx exec main-sbx herdr integration install claude": {Stdout: "installed\n"},
				"sbx exec main-sbx herdr server":                     {Stdout: "server started\n"},
				"sbx exec -e HERDR_ENV=1 -e HERDR_SOCKET_PATH=/home/agent/.config/herdr/herdr.sock -e HERDR_PANE_ID=sandbox-claude main-sbx claude": {Stdout: "claude started\n"},
			},
		},
		sequences: map[string][]CommandResult{
			"sbx exec main-sbx herdr status server --json": {
				{Stdout: `{"running":false,"socket":"/home/agent/.config/herdr/herdr.sock"}` + "\n"},
				{Stdout: `{"running":true,"socket":"/home/agent/.config/herdr/herdr.sock"}` + "\n"},
			},
		},
	}

	_, err := (DockerSandbox{Runner: &runner, Binary: "sbx"}).StartMain(context.Background(), NewStartPlan(herdrConfig()))

	if err != nil {
		t.Fatalf("expected Herdr readiness wait to succeed: %v", err)
	}
	got := strings.Join(calls, "\n")
	want := strings.Join([]string{
		"sbx exec main-sbx herdr status server --json",
		"sbx exec main-sbx herdr status server --json",
		"sbx exec -e HERDR_ENV=1 -e HERDR_SOCKET_PATH=/home/agent/.config/herdr/herdr.sock -e HERDR_PANE_ID=sandbox-claude main-sbx claude",
	}, "\n")
	if !strings.Contains(got, want) {
		t.Fatalf("expected Claude to start after repeated readiness checks, got:\n%s", got)
	}
}

func TestDockerSandboxStartMainFailsClosedWhenSandboxLocalHerdrReadinessFails(t *testing.T) {
	tests := []struct {
		name         string
		status       CommandResult
		statusErr    error
		wantErr      string
		shortTimeout bool
	}{
		{
			name:         "timeout",
			status:       CommandResult{Stdout: `{"running":false,"socket":"/home/agent/.config/herdr/herdr.sock"}` + "\n"},
			wantErr:      "timed out",
			shortTimeout: true,
		},
		{
			name:      "status command failure",
			status:    CommandResult{Stderr: "status failed\n"},
			statusErr: errors.New("exit status 1"),
			wantErr:   "status failed",
		},
		{
			name:    "socket mismatch",
			status:  CommandResult{Stdout: `{"running":true,"socket":"/tmp/wrong-herdr.sock"}` + "\n"},
			wantErr: "socket path mismatch",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := []string{}
			runner := stubRunner{
				path:  "/tmp/sbx",
				calls: &calls,
				results: map[string]CommandResult{
					"sbx ls": {Stdout: "No sandboxes found.\n"},
					"sbx create --name main-sbx claude /work/project":    {Stdout: "created\n"},
					"sbx exec main-sbx command -v herdr":                 {Stdout: "/home/agent/.local/bin/herdr\n"},
					"sbx exec main-sbx herdr --version":                  {Stdout: "herdr 0.7.1\n"},
					"sbx exec main-sbx herdr integration install claude": {Stdout: "installed\n"},
					"sbx exec main-sbx herdr server":                     {Stdout: "server started\n"},
					"sbx exec main-sbx herdr status server --json":       tt.status,
					"sbx exec main-sbx herdr server stop":                {Stdout: "stopped\n"},
					"sbx stop main-sbx":                                  {Stdout: "stopped\n"},
				},
				errors: map[string]error{
					"sbx exec main-sbx herdr status server --json": tt.statusErr,
				},
			}
			plan := NewStartPlan(herdrConfig())
			if tt.shortTimeout {
				plan.Supervision.Herdr.ReadinessTimeout = time.Nanosecond
			}

			_, err := (DockerSandbox{Runner: runner, Binary: "sbx"}).StartMain(context.Background(), plan)

			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("expected readiness error containing %q, got %v", tt.wantErr, err)
			}
			got := strings.Join(calls, "\n")
			if strings.Contains(got, "HERDR_ENV=1") {
				t.Fatalf("Claude should not start after readiness failure, got:\n%s", got)
			}
			if !strings.Contains(got, "sbx exec main-sbx herdr server stop\nsbx stop main-sbx") {
				t.Fatalf("expected Herdr server and main sandbox cleanup after readiness failure, got:\n%s", got)
			}
		})
	}
}

func TestDockerSandboxStartMainRebuildsStoppedSandboxLocalHerdrMain(t *testing.T) {
	calls := []string{}
	runner := stubRunner{
		path:  "/tmp/sbx",
		calls: &calls,
		results: map[string]CommandResult{
			"sbx ls":                  {Stdout: "SANDBOX    AGENT    STATUS    PORTS    WORKSPACE\nmain-sbx   claude   stopped            /work/project\n"},
			"sbx stop main-sbx":       {Stdout: "Sandbox 'main-sbx' stopped\n"},
			"sbx rm --force main-sbx": {Stdout: "Sandbox 'main-sbx' removed\n"},
			"sbx create --name main-sbx claude /work/project":    {Stdout: "created\n"},
			"sbx exec main-sbx command -v herdr":                 {Stdout: "/home/agent/.local/bin/herdr\n"},
			"sbx exec main-sbx herdr --version":                  {Stdout: "herdr 0.7.1\n"},
			"sbx exec main-sbx herdr integration install claude": {Stdout: "installed\n"},
			"sbx exec main-sbx herdr server":                     {Stdout: "server started\n"},
			"sbx exec main-sbx herdr status server --json":       {Stdout: `{"running":true,"socket":"/home/agent/.config/herdr/herdr.sock"}` + "\n"},
			"sbx exec -e HERDR_ENV=1 -e HERDR_SOCKET_PATH=/home/agent/.config/herdr/herdr.sock -e HERDR_PANE_ID=sandbox-claude main-sbx claude": {Stdout: "claude started\n"},
		},
	}

	if _, err := (DockerSandbox{Runner: runner, Binary: "sbx"}).StartMain(context.Background(), NewStartPlan(herdrConfig())); err != nil {
		t.Fatalf("expected stopped main sandbox to be rebuilt safely: %v", err)
	}

	got := strings.Join(calls, "\n")
	want := strings.Join([]string{
		"sbx ls",
		"sbx stop main-sbx",
		"sbx rm --force main-sbx",
		"sbx create --name main-sbx claude /work/project",
		"sbx exec main-sbx command -v herdr",
	}, "\n")
	if !strings.Contains(got, want) {
		t.Fatalf("expected stopped main sandbox rebuild before Herdr setup, got:\n%s", got)
	}
}

func TestDockerSandboxStartMainRejectsUnsafeExistingSandboxWithoutStoppingIt(t *testing.T) {
	calls := []string{}
	runner := stubRunner{
		path:  "/tmp/sbx",
		calls: &calls,
		results: map[string]CommandResult{
			"sbx ls": {Stdout: "SANDBOX    AGENT    STATUS    PORTS    WORKSPACE\nmain-sbx   claude   running            /work/project\n"},
		},
	}

	_, err := (DockerSandbox{Runner: runner, Binary: "sbx"}).StartMain(context.Background(), NewStartPlan(herdrConfig()))

	if err == nil {
		t.Fatalf("expected running existing main sandbox to fail closed")
	}
	if !strings.Contains(err.Error(), "unsafe status") || strings.Contains(err.Error(), "/work/project") {
		t.Fatalf("expected actionable non-sensitive diagnostic, got %v", err)
	}
	for _, call := range calls {
		if call == "sbx exec main-sbx herdr server stop" || call == "sbx stop main-sbx" || call == "sbx rm --force main-sbx" {
			t.Fatalf("unsafe existing sandbox should not be cleaned up by startup path, got:\n%s", strings.Join(calls, "\n"))
		}
	}
}

func TestDockerSandboxStartMainStopsSandboxLocalHerdrWhenClaudeStartFails(t *testing.T) {
	calls := []string{}
	runner := stubRunner{
		path:  "/tmp/sbx",
		calls: &calls,
		results: map[string]CommandResult{
			"sbx ls": {Stdout: "No sandboxes found.\n"},
			"sbx create --name main-sbx claude /work/project":    {Stdout: "created\n"},
			"sbx exec main-sbx command -v herdr":                 {Stdout: "/home/agent/.local/bin/herdr\n"},
			"sbx exec main-sbx herdr --version":                  {Stdout: "herdr 0.7.1\n"},
			"sbx exec main-sbx herdr integration install claude": {Stdout: "installed\n"},
			"sbx exec main-sbx herdr server":                     {Stdout: "server started\n"},
			"sbx exec -e HERDR_ENV=1 -e HERDR_SOCKET_PATH=/home/agent/.config/herdr/herdr.sock -e HERDR_PANE_ID=sandbox-claude main-sbx claude": {Stderr: "claude failed\n"},
			"sbx exec main-sbx herdr server stop": {Stdout: "stopped\n"},
			"sbx stop main-sbx":                   {Stdout: "stopped\n"},
		},
		errors: map[string]error{
			"sbx exec -e HERDR_ENV=1 -e HERDR_SOCKET_PATH=/home/agent/.config/herdr/herdr.sock -e HERDR_PANE_ID=sandbox-claude main-sbx claude": errors.New("exit status 1"),
		},
	}

	_, err := (DockerSandbox{Runner: runner, Binary: "sbx"}).StartMain(context.Background(), NewStartPlan(herdrConfig()))

	if err == nil {
		t.Fatalf("expected Claude startup failure")
	}
	got := strings.Join(calls, "\n")
	if !strings.Contains(got, "sbx exec main-sbx herdr server stop\nsbx stop main-sbx") {
		t.Fatalf("expected Herdr server and main sandbox cleanup after Claude failure, got:\n%s", got)
	}
}

func TestDockerSandboxProbeAllowsSandboxLocalHerdrInspectionWhenConfigured(t *testing.T) {
	cfg := herdrConfig()
	cfg.Sandbox.ProbeName = "probe"
	cfg.Workspace.Mount = "."
	runner := probeRunner(
		"PATH=/usr/bin\nHERDR_ENV=1\nHERDR_SOCKET_PATH=/home/agent/.config/herdr/herdr.sock\nHERDR_PANE_ID=sandbox-claude\n",
		"/dev/disk1 on /workspace type virtiofs (rw,source=/Users/alice/work/safe-claude-sbx)\n",
	)

	result, err := (DockerSandbox{Runner: runner, Binary: "sbx"}).Probe(context.Background(), cfg)

	if err != nil {
		t.Fatalf("expected sandbox-local Herdr inspection to pass: %v", err)
	}
	if !result.CleanupDone {
		t.Fatalf("expected probe cleanup to be marked done")
	}
}

func TestDockerSandboxStartMainInstallsMissingSandboxLocalHerdrWhenConfigured(t *testing.T) {
	calls := []string{}
	runner := stubRunner{
		path:  "/tmp/sbx",
		calls: &calls,
		results: map[string]CommandResult{
			"sbx create --name main-sbx claude /work/project":                       {Stdout: "created\n"},
			"sbx exec main-sbx command -v herdr":                                    {},
			"sbx exec main-sbx sh -lc curl -fsSL https://herdr.dev/install.sh | sh": {Stdout: "installed\n"},
			"sbx exec main-sbx herdr --version":                                     {Stdout: "herdr 0.7.1\n"},
			"sbx exec main-sbx herdr integration install claude":                    {Stdout: "installed\n"},
			"sbx exec main-sbx herdr server":                                        {Stdout: "server started\n"},
			"sbx exec main-sbx herdr status server --json":                          {Stdout: `{"running":true,"socket":"/home/agent/.config/herdr/herdr.sock"}` + "\n"},
			"sbx exec -e HERDR_ENV=1 -e HERDR_SOCKET_PATH=/home/agent/.config/herdr/herdr.sock -e HERDR_PANE_ID=sandbox-claude main-sbx claude": {Stdout: "claude started\n"},
		},
		errors: map[string]error{
			"sbx exec main-sbx command -v herdr": errors.New("exit status 127"),
		},
	}
	cfg := herdrConfig()

	if _, err := (DockerSandbox{Runner: runner, Binary: "sbx"}).StartMain(context.Background(), NewStartPlan(cfg)); err != nil {
		t.Fatalf("expected install-if-missing Herdr startup to succeed: %v", err)
	}
	if !runner.saw("sbx exec main-sbx sh -lc curl -fsSL https://herdr.dev/install.sh | sh") {
		t.Fatalf("expected missing Herdr install command, got %#v", runner.keys())
	}
}

func TestDockerSandboxStartMainTimesOutSandboxLocalHerdrInstallAndCleansUp(t *testing.T) {
	runner := &herdrInstallTimeoutRunner{}
	plan := NewStartPlan(herdrConfig())
	plan.Supervision.Herdr.InstallTimeout = time.Nanosecond
	plan.Supervision.Herdr.InstallAttempts = 1

	_, err := (DockerSandbox{Runner: runner, Binary: "sbx"}).StartMain(context.Background(), plan)

	if err == nil {
		t.Fatalf("expected Herdr install timeout")
	}
	if !strings.Contains(err.Error(), "install sandbox-local Herdr") || !strings.Contains(err.Error(), "timed out after") || !strings.Contains(err.Error(), "attempt 1/1") {
		t.Fatalf("expected bounded install diagnostic, got %v", err)
	}
	if !runner.installHadDeadline {
		t.Fatalf("Herdr install did not receive a bounded context")
	}
	if got := strings.Join(runner.calls, "\n"); !strings.Contains(got, "sbx stop main-sbx") {
		t.Fatalf("expected main sandbox cleanup after install timeout, got:\n%s", got)
	}
	if strings.Contains(err.Error(), "/tmp/host-herdr.sock") {
		t.Fatalf("install error leaked host Herdr state: %v", err)
	}
}

func TestDockerSandboxStartMainRetriesSandboxLocalHerdrInstallUntilSuccess(t *testing.T) {
	runner := &herdrInstallAttemptRunner{
		installResults: []CommandResult{
			{Stderr: "download failed\n"},
			{Stdout: "installed\n"},
		},
		installErrors: []error{
			errors.New("exit status 28"),
			nil,
		},
	}
	plan := NewStartPlan(herdrConfig())
	plan.Supervision.Herdr.InstallAttempts = 2

	if _, err := (DockerSandbox{Runner: runner, Binary: "sbx"}).StartMain(context.Background(), plan); err != nil {
		t.Fatalf("expected Herdr install retry to succeed: %v", err)
	}
	if runner.installCalls != 2 {
		t.Fatalf("expected two install attempts, got %d with calls:\n%s", runner.installCalls, strings.Join(runner.calls, "\n"))
	}
	if got := strings.Join(runner.calls, "\n"); !strings.Contains(got, "sbx exec -e HERDR_ENV=1") {
		t.Fatalf("expected Claude to start after retry succeeds, got:\n%s", got)
	}
}

func TestDockerSandboxStartMainFailsClosedWhenSandboxLocalHerdrInstallRetriesExhausted(t *testing.T) {
	runner := &herdrInstallAttemptRunner{
		installResults: []CommandResult{
			{Stderr: "download failed\n"},
			{Stderr: "download failed\n"},
		},
		installErrors: []error{
			errors.New("exit status 28"),
			errors.New("exit status 28"),
		},
	}
	plan := NewStartPlan(herdrConfig())
	plan.Supervision.Herdr.InstallAttempts = 2

	_, err := (DockerSandbox{Runner: runner, Binary: "sbx"}).StartMain(context.Background(), plan)

	if err == nil {
		t.Fatalf("expected Herdr install retries to fail")
	}
	if !strings.Contains(err.Error(), "attempt 2/2 failed") || !strings.Contains(err.Error(), "download failed") {
		t.Fatalf("expected retry exhausted diagnostic, got %v", err)
	}
	got := strings.Join(runner.calls, "\n")
	if strings.Contains(got, "HERDR_ENV=1") {
		t.Fatalf("Claude should not start after install retries are exhausted, got:\n%s", got)
	}
	if !strings.Contains(got, "sbx exec main-sbx herdr server stop\nsbx stop main-sbx") {
		t.Fatalf("expected Herdr server and main sandbox cleanup after install failure, got:\n%s", got)
	}
	if strings.Contains(err.Error(), "/tmp/host-herdr.sock") {
		t.Fatalf("install error leaked host Herdr state: %v", err)
	}
}

func TestDockerSandboxCleanupMainStopsSandboxLocalHerdrBeforeSandbox(t *testing.T) {
	calls := []string{}
	runner := stubRunner{
		path:  "/tmp/sbx",
		calls: &calls,
		results: map[string]CommandResult{
			"sbx exec main-sbx herdr server stop": {Stdout: "stopped\n"},
			"sbx stop main-sbx":                   {Stdout: "sandbox stopped\n"},
		},
	}
	cfg := herdrConfig()
	cfg.Cleanup.StopMainSandbox = true

	err := (DockerSandbox{Runner: runner, Binary: "sbx"}).CleanupMain(context.Background(), cfg)

	if err != nil {
		t.Fatalf("cleanup main failed: %v", err)
	}
	if got := strings.Join(runner.keys(), "\n"); !strings.Contains(got, "sbx exec main-sbx herdr server stop\nsbx stop main-sbx") {
		t.Fatalf("expected Herdr cleanup before sandbox stop, got:\n%s", got)
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
			name:      "host Herdr config",
			env:       "PATH=/usr/bin\nHERDR_CONFIG_DIR=/Users/alice/.config/herdr\n",
			mounts:    "/dev/disk1 on /workspace type virtiofs (rw,source=/Users/alice/work/safe-claude-sbx)\n",
			wantError: "environment.inspection.env.HERDR_CONFIG_DIR",
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

			result, err := (DockerSandbox{Runner: runner, Binary: "sbx"}).Probe(context.Background(), cfg)

			if err == nil {
				t.Fatalf("probe unexpectedly succeeded")
			}
			if !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("expected %q in error, got %v", tt.wantError, err)
			}
			if strings.Contains(err.Error(), "127.0.0.1:7897") || strings.Contains(err.Error(), "/Users/alice/.ssh/agent.sock") || strings.Contains(err.Error(), "/Users/alice/.config/herdr") {
				t.Fatalf("probe error leaked sensitive value: %v", err)
			}
			if !result.CleanupDone {
				t.Fatalf("expected unsafe inspection to trigger probe cleanup")
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

func herdrConfig() config.Config {
	cfg := probeConfig()
	cfg.Sandbox.MainName = "main-sbx"
	cfg.Workspace.Mount = "/work/project"
	installIfMissing := true
	cfg.Sandbox.Supervision = config.Supervision{
		Mode: "sandbox-local-herdr",
		Herdr: &config.HerdrSupervision{
			InstallIfMissing: &installIfMissing,
			SocketPath:       "/home/agent/.config/herdr/herdr.sock",
			PaneID:           "sandbox-claude",
		},
	}
	return cfg
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
	calls       *[]string
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
	if r.calls != nil {
		*r.calls = append(*r.calls, key)
	}
	result := r.results[key]
	return result, r.errors[key]
}

func (r stubRunner) keys() []string {
	if r.calls != nil {
		return append([]string(nil), (*r.calls)...)
	}
	keys := make([]string, 0, len(r.results))
	for key := range r.results {
		keys = append(keys, key)
	}
	return keys
}

func (r stubRunner) saw(key string) bool {
	for _, got := range r.keys() {
		if got == key {
			return true
		}
	}
	return false
}

type sequenceRunner struct {
	stubRunner
	sequences map[string][]CommandResult
	counts    map[string]int
}

func (r *sequenceRunner) Run(ctx context.Context, name string, args ...string) (CommandResult, error) {
	key := strings.Join(append([]string{name}, args...), " ")
	if r.calls != nil {
		*r.calls = append(*r.calls, key)
	}
	if sequence := r.sequences[key]; len(sequence) > 0 {
		if r.counts == nil {
			r.counts = map[string]int{}
		}
		index := r.counts[key]
		r.counts[key] = index + 1
		if index >= len(sequence) {
			index = len(sequence) - 1
		}
		return sequence[index], nil
	}
	result := r.results[key]
	return result, r.errors[key]
}

type herdrInstallTimeoutRunner struct {
	calls              []string
	installHadDeadline bool
}

func (r *herdrInstallTimeoutRunner) LookPath(file string) (string, error) {
	return file, nil
}

func (r *herdrInstallTimeoutRunner) Run(ctx context.Context, name string, args ...string) (CommandResult, error) {
	key := strings.Join(append([]string{name}, args...), " ")
	r.calls = append(r.calls, key)
	switch key {
	case "sbx ls":
		return CommandResult{Stdout: "No sandboxes found.\n"}, nil
	case "sbx create --name main-sbx claude /work/project":
		return CommandResult{Stdout: "created\n"}, nil
	case "sbx exec main-sbx command -v herdr":
		return CommandResult{}, errors.New("exit status 127")
	case "sbx exec main-sbx sh -lc curl -fsSL https://herdr.dev/install.sh | sh":
		_, r.installHadDeadline = ctx.Deadline()
		if !r.installHadDeadline {
			return CommandResult{}, errors.New("install used unbounded context")
		}
		<-ctx.Done()
		return CommandResult{Stderr: "downloading v0.7.1\n"}, ctx.Err()
	case "sbx exec main-sbx herdr server stop":
		return CommandResult{Stderr: "command not found\n"}, errors.New("exit status 127")
	case "sbx stop main-sbx":
		return CommandResult{Stdout: "stopped\n"}, nil
	default:
		return CommandResult{}, fmt.Errorf("unexpected command %q", key)
	}
}

type herdrInstallAttemptRunner struct {
	calls          []string
	installCalls   int
	installResults []CommandResult
	installErrors  []error
}

func (r *herdrInstallAttemptRunner) LookPath(file string) (string, error) {
	return file, nil
}

func (r *herdrInstallAttemptRunner) Run(ctx context.Context, name string, args ...string) (CommandResult, error) {
	key := strings.Join(append([]string{name}, args...), " ")
	r.calls = append(r.calls, key)
	switch key {
	case "sbx ls":
		return CommandResult{Stdout: "No sandboxes found.\n"}, nil
	case "sbx create --name main-sbx claude /work/project":
		return CommandResult{Stdout: "created\n"}, nil
	case "sbx exec main-sbx command -v herdr":
		return CommandResult{}, errors.New("exit status 127")
	case "sbx exec main-sbx sh -lc curl -fsSL https://herdr.dev/install.sh | sh":
		index := r.installCalls
		r.installCalls++
		if index >= len(r.installResults) {
			return CommandResult{}, fmt.Errorf("unexpected install attempt %d", index+1)
		}
		var err error
		if index < len(r.installErrors) {
			err = r.installErrors[index]
		}
		return r.installResults[index], err
	case "sbx exec main-sbx herdr --version":
		return CommandResult{Stdout: "herdr 0.7.1\n"}, nil
	case "sbx exec main-sbx herdr integration install claude":
		return CommandResult{Stdout: "installed\n"}, nil
	case "sbx exec main-sbx herdr server":
		return CommandResult{Stdout: "server started\n"}, nil
	case "sbx exec main-sbx herdr status server --json":
		return CommandResult{Stdout: `{"running":true,"socket":"/home/agent/.config/herdr/herdr.sock"}` + "\n"}, nil
	case "sbx exec -e HERDR_ENV=1 -e HERDR_SOCKET_PATH=/home/agent/.config/herdr/herdr.sock -e HERDR_PANE_ID=sandbox-claude main-sbx claude":
		return CommandResult{Stdout: "claude started\n"}, nil
	case "sbx exec main-sbx herdr server stop":
		return CommandResult{Stderr: "command not found\n"}, errors.New("exit status 127")
	case "sbx stop main-sbx":
		return CommandResult{Stdout: "stopped\n"}, nil
	default:
		return CommandResult{}, fmt.Errorf("unexpected command %q", key)
	}
}
