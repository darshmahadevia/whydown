package domain

import "time"

type Container struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Image  string `json:"image"`
	State  string `json:"state"`
	Status string `json:"status"`
}

type Evidence struct {
	Container    Container `json:"container"`
	CollectedAt  time.Time `json:"collected_at"`
	ExitCode     int       `json:"exit_code"`
	OOMKilled    bool      `json:"oom_killed"`
	Error        string    `json:"error,omitempty"`
	RestartCount int       `json:"restart_count"`
	Health       string    `json:"health,omitempty"`
	HealthLogs   []string  `json:"health_logs,omitempty"`
	Logs         string    `json:"logs"`
	Env          []string  `json:"env"`
	Command      []string  `json:"command,omitempty"`
	Networks     []string  `json:"networks,omitempty"`
	MemoryUsage  uint64    `json:"memory_usage_bytes"`
	MemoryLimit  uint64    `json:"memory_limit_bytes"`
	CPUPercent   float64   `json:"cpu_percent"`
	Warnings     []string  `json:"warnings,omitempty"`
}

type Report struct {
	Container  string   `json:"container"`
	Summary    string   `json:"summary"`
	Cause      string   `json:"cause"`
	Fixes      []string `json:"fixes"`
	Confidence string   `json:"confidence"`
	Evidence   []string `json:"evidence"`
	Provider   string   `json:"provider"`
}
