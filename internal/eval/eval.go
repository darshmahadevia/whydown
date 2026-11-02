package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"whydown/internal/domain"
)

type Case struct {
	Name     string          `json:"name"`
	Evidence domain.Evidence `json:"evidence"`
	Expected []string        `json:"expected"`
}
type Result struct {
	Name   string        `json:"name"`
	Passed bool          `json:"passed"`
	Error  string        `json:"error,omitempty"`
	Report domain.Report `json:"report"`
}
type Score struct {
	Mode    string   `json:"mode"`
	Passed  int      `json:"passed"`
	Total   int      `json:"total"`
	Results []Result `json:"results"`
}
type Diagnose func(context.Context, domain.Evidence) (domain.Report, error)

func Load(path string) ([]Case, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cases []Case
	if err = json.Unmarshal(data, &cases); err != nil {
		return nil, err
	}
	if len(cases) == 0 {
		return nil, fmt.Errorf("evaluation has no cases")
	}
	for _, c := range cases {
		if c.Name == "" || len(c.Expected) == 0 {
			return nil, fmt.Errorf("each evaluation case needs a name and expected terms")
		}
	}
	return cases, nil
}

func Run(ctx context.Context, mode string, cases []Case, diagnose Diagnose) Score {
	score := Score{Mode: mode, Total: len(cases), Results: make([]Result, 0, len(cases))}
	for _, c := range cases {
		report, err := diagnose(ctx, c.Evidence)
		result := Result{Name: c.Name, Report: report, Passed: err == nil}
		if err != nil {
			result.Error = err.Error()
		}
		text := strings.ToLower(report.Summary + " " + report.Cause + " " + strings.Join(report.Fixes, " "))
		for _, term := range c.Expected {
			if !strings.Contains(text, strings.ToLower(term)) {
				result.Passed = false
			}
		}
		if result.Passed {
			score.Passed++
		}
		score.Results = append(score.Results, result)
	}
	return score
}
