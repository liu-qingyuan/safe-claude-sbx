package watchdog

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

type RouteMonitor struct {
	Command string
}

func (m RouteMonitor) Start(ctx context.Context) (<-chan Event, <-chan error) {
	events := make(chan Event)
	errs := make(chan error, 1)

	go func() {
		defer close(events)
		defer close(errs)

		command := m.Command
		if command == "" {
			command = "route"
		}
		cmd := exec.CommandContext(ctx, command, "-n", "monitor")
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			errs <- fmt.Errorf("route monitor stdout: %w", err)
			return
		}
		if err := cmd.Start(); err != nil {
			errs <- fmt.Errorf("start route monitor: %w", err)
			return
		}

		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			detail := strings.TrimSpace(scanner.Text())
			if detail == "" {
				continue
			}
			select {
			case <-ctx.Done():
				_ = cmd.Wait()
				return
			case events <- Event{Source: "route-monitor", Detail: detail}:
			}
		}
		if err := scanner.Err(); err != nil && ctx.Err() == nil {
			errs <- fmt.Errorf("read route monitor: %w", err)
			_ = cmd.Wait()
			return
		}
		if err := cmd.Wait(); err != nil && ctx.Err() == nil {
			errs <- fmt.Errorf("route monitor exited: %w", err)
			return
		}
		if ctx.Err() == nil {
			errs <- fmt.Errorf("route monitor exited")
		}
	}()

	return events, errs
}
