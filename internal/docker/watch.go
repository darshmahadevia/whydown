package docker

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/moby/moby/api/types/events"
	"github.com/moby/moby/client"
	"whydown/internal/domain"
)

const scanInterval = 15 * time.Second
const restartWindow = time.Minute

type observation struct {
	baseline       int
	since          time.Time
	failure        string
	recentRestarts []time.Time
}

// Watch calls consume serially. Cancellation returns ctx.Err(); callback and
// Docker stream errors stop the watch. Callbacks must honor their own cancellation.
func (c *Client) Watch(ctx context.Context, consume func(domain.Evidence) error) error {
	if consume == nil {
		return fmt.Errorf("Docker watch requires an evidence callback")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	startup := time.AfterFunc(requestTimeout, cancel)
	stream := c.api.Events(ctx, client.EventsListOptions{Since: time.Now().UTC().Format(time.RFC3339Nano), Filters: make(client.Filters).Add("type", "container").Add("event", "die", "oom", "health_status", "restart")})
	startup.Stop()
	seen := make(map[string]observation)
	if err := c.scan(ctx, seen, consume); err != nil {
		return err
	}
	ticker := time.NewTicker(scanInterval)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err, ok := <-stream.Err:
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if !ok || err == nil {
				err = io.EOF
			}
			return fmt.Errorf("Docker event stream stopped: %w", err)
		case event := <-stream.Messages:
			if event.Type != events.ContainerEventType || event.Actor.ID == "" {
				continue
			}
			report := false
			switch event.Action {
			case events.ActionDie, events.ActionOOM, events.ActionHealthStatusUnhealthy:
				report = true
			case events.ActionRestart:
				now := time.Now()
				old := seen[event.Actor.ID]
				recent := old.recentRestarts[:0]
				for _, at := range old.recentRestarts {
					if now.Sub(at) <= restartWindow {
						recent = append(recent, at)
					}
				}
				recent = append(recent, now)
				report = len(recent) >= 3
				if len(recent) > 3 {
					recent = recent[len(recent)-3:]
				}
				old.recentRestarts = recent
				// Unknown IDs are bounded between scans even on very busy daemons.
				if len(seen) < 4096 {
					seen[event.Actor.ID] = old
				} else if _, ok := seen[event.Actor.ID]; ok {
					seen[event.Actor.ID] = old
				}
			}
			if !report {
				continue
			}
			evidence, err := c.Collect(ctx, event.Actor.ID)
			if gone(err) {
				continue
			}
			if err != nil {
				return err
			}
			if event.Action == events.ActionOOM {
				evidence.OOMKilled = true
			}
			if event.Action == events.ActionDie {
				if code, err := strconv.Atoi(event.Actor.Attributes["exitCode"]); err == nil {
					evidence.ExitCode = code
				}
			}
			evidence.Warnings = append(evidence.Warnings, "Triggered by Docker event "+string(event.Action)+"; inspection reflects the current container state.")
			if err := consume(evidence); err != nil {
				return err
			}
		case <-ticker.C:
			if err := c.scan(ctx, seen, consume); err != nil {
				return err
			}
		}
	}
}

func failed(e domain.Evidence) bool {
	return e.Error != "" || e.Health == "unhealthy" || e.Container.State == "restarting" || e.Container.State == "dead" || e.OOMKilled || (e.Container.State == "exited" && e.ExitCode != 0)
}

func (c *Client) scan(ctx context.Context, seen map[string]observation, consume func(domain.Evidence) error) error {
	containers, err := c.List(ctx)
	if err != nil {
		return err
	}
	present := make(map[string]bool, len(containers))
	now := time.Now()
	for _, item := range containers {
		info, err := c.inspect(ctx, item.ID)
		if gone(err) {
			continue
		}
		if err != nil {
			return err
		}
		present[item.ID] = true
		e := fromInspect(info)
		old, known := seen[item.ID]
		if !known {
			old = observation{baseline: e.RestartCount, since: now}
		}
		loop := known && now.Sub(old.since) <= restartWindow && e.RestartCount-old.baseline >= 3
		signature := ""
		if failed(e) {
			signature = fmt.Sprintf("%s/%s/%d/%t/%d/%s", e.Container.State, e.Health, e.ExitCode, e.OOMKilled, e.RestartCount, e.Error)
		}
		if loop || (signature != "" && signature != old.failure) {
			evidence, err := c.Collect(ctx, item.ID)
			if gone(err) {
				continue
			}
			if err != nil {
				return err
			}
			if loop {
				evidence.Warnings = append(evidence.Warnings, "Container restarted at least 3 times within one minute.")
			}
			if err := consume(evidence); err != nil {
				return err
			}
		}
		if loop || now.Sub(old.since) >= restartWindow || e.RestartCount < old.baseline {
			old.baseline = e.RestartCount
			old.since = now
		}
		old.failure = signature
		seen[item.ID] = old
	}
	for id := range seen {
		if !present[id] {
			delete(seen, id)
		}
	}
	return nil
}
