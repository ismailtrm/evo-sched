package measurement

import (
	"fmt"
	"time"
)

const (
	Version                      = "v2"
	SubmittedAtAnnotation        = "evo-sched.io/submitted-at"
	DecisionAtAnnotation         = "evo-sched.io/decision-at"
	MeasurementVersionAnnotation = "evo-sched.io/measurement-version"
	PolicyVersionAnnotation      = "evo-sched.io/policy-version"
)

func FormatTimestamp(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func ParseTimestamp(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse measurement timestamp %q: %w", value, err)
	}
	return parsed.UTC(), nil
}
