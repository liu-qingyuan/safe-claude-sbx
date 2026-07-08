package watchdog

import (
	"context"
	"sync"
)

func MergeEventStreams(ctx context.Context, eventStreams []<-chan Event, errorStreams []<-chan error) (<-chan Event, <-chan error) {
	events := make(chan Event)
	errs := make(chan error, len(errorStreams))

	var wg sync.WaitGroup
	for _, stream := range eventStreams {
		if stream == nil {
			continue
		}
		wg.Add(1)
		go func(stream <-chan Event) {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case event, ok := <-stream:
					if !ok {
						return
					}
					select {
					case <-ctx.Done():
						return
					case events <- event:
					}
				}
			}
		}(stream)
	}
	for _, stream := range errorStreams {
		if stream == nil {
			continue
		}
		wg.Add(1)
		go func(stream <-chan error) {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case err, ok := <-stream:
					if !ok {
						return
					}
					select {
					case <-ctx.Done():
						return
					case errs <- err:
					}
				}
			}
		}(stream)
	}

	go func() {
		wg.Wait()
		close(events)
		close(errs)
	}()

	return events, errs
}
