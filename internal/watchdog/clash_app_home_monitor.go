package watchdog

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/liu-qingyuan/safe-claude-sbx/internal/config"
	"github.com/liu-qingyuan/safe-claude-sbx/internal/network"
)

const (
	DefaultClashAppHomePollInterval = 100 * time.Millisecond
	ClashAppHomeEventSource         = "clash-app-home"
)

var defaultClashAppHomeMetadataPaths = []string{
	"verge.yaml",
	"config.yaml",
	"clash-verge.yaml",
	"profiles.yaml",
	"profiles",
	"rules",
	"providers",
}

type ClashAppHomeMonitor struct {
	Policy       config.ClashVerge
	PollInterval time.Duration
	Paths        []string
}

func (m ClashAppHomeMonitor) Start(ctx context.Context) (<-chan Event, <-chan error) {
	events := make(chan Event)
	errs := make(chan error, 1)

	go func() {
		defer close(events)
		defer close(errs)

		appHome := network.ClashVergeAppHomeHostPath(m.Policy.AppHome)
		if err := validateClashAppHome(appHome); err != nil {
			errs <- err
			return
		}

		paths := m.Paths
		if len(paths) == 0 {
			paths = defaultClashAppHomeMetadataPaths
		}
		previous, err := snapshotClashAppHome(appHome, paths)
		if err != nil {
			errs <- err
			return
		}

		interval := m.PollInterval
		if interval <= 0 {
			interval = DefaultClashAppHomePollInterval
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				next, err := snapshotClashAppHome(appHome, paths)
				if err != nil {
					errs <- err
					return
				}
				changed := changedClashMetadata(previous, next)
				if len(changed) == 0 {
					previous = next
					continue
				}
				previous = next
				event := Event{
					Source: ClashAppHomeEventSource,
					Detail: "metadata changed: " + strings.Join(changed, ", "),
				}
				select {
				case <-ctx.Done():
					return
				case events <- event:
				}
			}
		}
	}()

	return events, errs
}

func validateClashAppHome(appHome string) error {
	info, err := os.Stat(appHome)
	if err != nil {
		return fmt.Errorf("clash app-home event source unavailable: network.clash_verge.app_home does not exist or is not accessible")
	}
	if !info.IsDir() {
		return fmt.Errorf("clash app-home event source unavailable: network.clash_verge.app_home is not a directory")
	}
	dir, err := os.Open(appHome)
	if err != nil {
		return fmt.Errorf("clash app-home event source unavailable: network.clash_verge.app_home is not accessible")
	}
	return dir.Close()
}

func snapshotClashAppHome(appHome string, paths []string) (map[string]clashMetadataState, error) {
	if err := validateClashAppHome(appHome); err != nil {
		return nil, err
	}
	snapshot := make(map[string]clashMetadataState, len(paths))
	for _, rel := range paths {
		clean := filepath.Clean(rel)
		if clean == "." || filepath.IsAbs(clean) || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || clean == ".." {
			continue
		}
		info, err := os.Stat(filepath.Join(appHome, clean))
		if err != nil {
			if os.IsNotExist(err) {
				snapshot[clean] = clashMetadataState{}
				continue
			}
			return nil, fmt.Errorf("clash app-home event source unavailable: cannot stat metadata for %s", filepath.ToSlash(clean))
		}
		snapshot[clean] = clashMetadataState{
			Exists:  true,
			Size:    info.Size(),
			Mode:    info.Mode(),
			ModTime: info.ModTime(),
			IsDir:   info.IsDir(),
		}
	}
	return snapshot, nil
}

func changedClashMetadata(previous, next map[string]clashMetadataState) []string {
	var changed []string
	for path, state := range next {
		if previous[path] != state {
			changed = append(changed, filepath.ToSlash(path))
		}
	}
	sort.Strings(changed)
	return changed
}

type clashMetadataState struct {
	Exists  bool
	Size    int64
	Mode    os.FileMode
	ModTime time.Time
	IsDir   bool
}
