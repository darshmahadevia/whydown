// Package docker collects read-only, bounded evidence from Docker Engine.
package docker

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/containerd/errdefs"
	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
	"whydown/internal/domain"
	"whydown/internal/redact"
)

const (
	requestTimeout  = 10 * time.Second
	statsTimeout    = 2 * time.Second
	maxLogBytes     = 64 * 1024
	maxLogWireBytes = 256 * 1024
	maxTextBytes    = 4096
	maxFieldBytes   = 16 * 1024
	maxFieldItems   = 128
)

type Client struct{ api *client.Client }

// New uses Docker's environment settings and automatic API negotiation.
func New() (*Client, error) {
	api, err := client.New(client.FromEnv)
	if err != nil {
		return nil, fmt.Errorf("configure Docker: %w", err)
	}
	return &Client{api: api}, nil
}
func (c *Client) Close() error { return c.api.Close() }

func (c *Client) List(ctx context.Context) ([]domain.Container, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	result, err := c.api.ContainerList(ctx, client.ContainerListOptions{All: true})
	if err != nil {
		return nil, fmt.Errorf("list Docker containers: %w", err)
	}
	out := make([]domain.Container, 0, len(result.Items))
	for _, v := range result.Items {
		name := v.ID
		if len(v.Names) > 0 {
			name = strings.TrimPrefix(v.Names[0], "/")
		}
		out = append(out, domain.Container{ID: v.ID, Name: name, Image: v.Image, State: string(v.State), Status: v.Status})
	}
	return out, nil
}

func (c *Client) inspect(ctx context.Context, id string) (container.InspectResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	result, err := c.api.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if err != nil {
		return container.InspectResponse{}, fmt.Errorf("inspect Docker container %q: %w", id, err)
	}
	return result.Container, nil
}

func (c *Client) Collect(ctx context.Context, id string) (domain.Evidence, error) {
	info, err := c.inspect(ctx, id)
	if err != nil {
		return domain.Evidence{}, err
	}
	e := fromInspect(info)
	c.logs(ctx, info, &e)
	if ctx.Err() != nil {
		return domain.Evidence{}, ctx.Err()
	}
	if info.State != nil && info.State.Running {
		c.stats(ctx, info.ID, &e)
	} else {
		e.Warnings = append(e.Warnings, "Live stats unavailable because the container is not running.")
	}
	if ctx.Err() != nil {
		return domain.Evidence{}, ctx.Err()
	}
	e = boundEvidence(redact.Evidence(e))
	return e, nil
}

func fromInspect(info container.InspectResponse) domain.Evidence {
	e := domain.Evidence{CollectedAt: time.Now().UTC(), Container: domain.Container{ID: info.ID, Name: strings.TrimPrefix(info.Name, "/"), Image: info.Image}, RestartCount: info.RestartCount}
	if info.State != nil {
		s := info.State
		e.Container.State = string(s.Status)
		e.Container.Status = string(s.Status)
		e.ExitCode = s.ExitCode
		e.OOMKilled = s.OOMKilled
		e.Error = s.Error
		if s.Health != nil {
			e.Health = string(s.Health.Status)
			logs := s.Health.Log
			for _, v := range logs {
				if v != nil {
					e.HealthLogs = append(e.HealthLogs, fmt.Sprintf("exit %d: %s", v.ExitCode, v.Output))
				}
			}
		}
	}
	if info.HostConfig != nil && info.HostConfig.Memory > 0 {
		e.MemoryLimit = uint64(info.HostConfig.Memory)
	}
	if info.Config != nil {
		e.Container.Image = info.Config.Image
		e.Env = info.Config.Env
	}
	e.Command = append([]string{info.Path}, info.Args...)
	if info.NetworkSettings != nil {
		for name := range info.NetworkSettings.Networks {
			e.Networks = append(e.Networks, name)
		}
		sort.Strings(e.Networks)
	}
	return e
}

func boundEvidence(e domain.Evidence) domain.Evidence {
	e.Container.ID = clip(e.Container.ID, maxTextBytes)
	e.Container.Name = clip(e.Container.Name, maxTextBytes)
	e.Container.Image = clip(e.Container.Image, maxTextBytes)
	e.Error = clip(e.Error, maxTextBytes)
	e.Env = boundedFields(e.Env, &e)
	e.Command = boundedFields(e.Command, &e)
	e.Networks = boundedFields(e.Networks, &e)
	if len(e.HealthLogs) > 5 {
		e.HealthLogs = e.HealthLogs[len(e.HealthLogs)-5:]
		e.Warnings = append(e.Warnings, "Health history limited to the last 5 entries.")
	}
	for i := range e.HealthLogs {
		e.HealthLogs[i] = clip(e.HealthLogs[i], maxTextBytes)
	}
	if len(e.Logs) > maxLogBytes {
		e.Logs = strings.ToValidUTF8(e.Logs[len(e.Logs)-maxLogBytes:], "")
		e.Warnings = append(e.Warnings, "Container logs limited to 64 KiB.")
	}
	for i := range e.Warnings {
		e.Warnings[i] = clip(e.Warnings[i], maxTextBytes)
	}
	return e
}

func boundedFields(values []string, e *domain.Evidence) []string {
	out := make([]string, 0, min(len(values), maxFieldItems))
	left := maxFieldBytes
	for _, v := range values {
		if len(out) == maxFieldItems || left == 0 {
			e.Warnings = append(e.Warnings, "Container metadata truncated to evidence limits.")
			break
		}
		value := clip(v, min(left, maxTextBytes))
		if len(value) < len(v) {
			e.Warnings = append(e.Warnings, "Container metadata value truncated.")
		}
		out = append(out, value)
		left -= len(value)
	}
	return out
}
func clip(s string, n int) string {
	if len(s) > n {
		return strings.ToValidUTF8(s[:n], "")
	}
	return strings.ToValidUTF8(s, "")
}

func (c *Client) logs(ctx context.Context, info container.InspectResponse, e *domain.Evidence) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	stream, err := c.api.ContainerLogs(ctx, info.ID, client.ContainerLogsOptions{ShowStdout: true, ShowStderr: true, Timestamps: true, Tail: "200"})
	if err != nil {
		e.Warnings = append(e.Warnings, "Container logs unavailable: "+err.Error())
		return
	}
	defer stream.Close()
	raw, err := io.ReadAll(io.LimitReader(stream, maxLogWireBytes+1))
	if err != nil {
		e.Warnings = append(e.Warnings, "Incomplete container log response omitted: "+err.Error())
		return
	}
	if len(raw) > maxLogWireBytes {
		e.Warnings = append(e.Warnings, "Container log response exceeded wire limit and was omitted to avoid exposing partial credentials.")
		return
	}
	var output bytes.Buffer
	if info.Config != nil && info.Config.Tty {
		output.Write(raw)
	} else {
		// Validate frame sizes before StdCopy, which allocates the advertised size.
		frames := raw
		end := 0
		for len(frames) >= 8 {
			size := int64(binary.BigEndian.Uint32(frames[4:8]))
			available := len(frames) - 8
			if size > int64(available) {
				e.Warnings = append(e.Warnings, "Incomplete container log response omitted.")
				return
			}
			step := 8 + int(size)
			end += step
			frames = frames[step:]
		}
		if len(frames) != 0 {
			e.Warnings = append(e.Warnings, "Incomplete container log header omitted with log response.")
			return
		}
		if _, err := stdcopy.StdCopy(&output, &output, bytes.NewReader(raw[:end])); err != nil {
			e.Warnings = append(e.Warnings, "Cannot decode container logs: "+err.Error())
		}
	}
	e.Logs = strings.ToValidUTF8(output.String(), "")
}

func (c *Client) stats(ctx context.Context, id string, e *domain.Evidence) {
	ctx, cancel := context.WithTimeout(ctx, statsTimeout)
	defer cancel()
	result, err := c.api.ContainerStats(ctx, id, client.ContainerStatsOptions{IncludePreviousSample: true})
	if err != nil {
		e.Warnings = append(e.Warnings, "Live stats unavailable: "+err.Error())
		return
	}
	defer result.Body.Close()
	var stats container.StatsResponse
	if err := json.NewDecoder(io.LimitReader(result.Body, 1024*1024)).Decode(&stats); err != nil {
		e.Warnings = append(e.Warnings, "Live stats unavailable: "+err.Error())
		return
	}
	e.MemoryUsage = stats.MemoryStats.Usage
	e.MemoryLimit = stats.MemoryStats.Limit
	cpu, prev := stats.CPUStats, stats.PreCPUStats
	if prev.SystemUsage > 0 && cpu.CPUUsage.TotalUsage >= prev.CPUUsage.TotalUsage && cpu.SystemUsage > prev.SystemUsage {
		cores := cpu.OnlineCPUs
		if cores == 0 {
			cores = uint32(len(cpu.CPUUsage.PercpuUsage))
		}
		e.CPUPercent = float64(cpu.CPUUsage.TotalUsage-prev.CPUUsage.TotalUsage) / float64(cpu.SystemUsage-prev.SystemUsage) * float64(cores) * 100
	}
}

func gone(err error) bool { return errors.Is(err, errdefs.ErrNotFound) }
