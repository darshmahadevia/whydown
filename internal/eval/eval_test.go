package eval

import (
	"context"
	"errors"
	"testing"
	"whydown/internal/domain"
)

func TestScoreCountsErrorsAndWrongAnswers(t *testing.T) {
	cases := []Case{{Name: "correct", Expected: []string{"memory", "limit"}}, {Name: "wrong", Expected: []string{"permission"}}, {Name: "error", Expected: []string{"memory"}}}
	calls := 0
	s := Run(context.Background(), "test", cases, func(context.Context, domain.Evidence) (domain.Report, error) {
		calls++
		if calls == 3 {
			return domain.Report{}, errors.New("unavailable")
		}
		return domain.Report{Summary: "memory limit"}, nil
	})
	if calls != 3 || s.Total != 3 || s.Passed != 1 || s.Results[2].Error == "" {
		t.Fatalf("incorrect score %#v, calls %d", s, calls)
	}
}
