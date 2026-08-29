package planner

import (
	"regexp"
	"strings"
)

// Signal markers are the handshake between the model and the orchestrator.
// They mirror the embedded plan-runner <<<PLANNER:...>>> protocol.
const (
	SignalAllTasksDone = "PLANNER:ALL_TASKS_DONE"
	SignalTaskFailed   = "PLANNER:TASK_FAILED"
	SignalReviewDone   = "PLANNER:REVIEW_DONE"
	SignalPlanReady    = "PLANNER:PLAN_READY"
)

var signalRe = regexp.MustCompile(`<<<PLANNER:([A-Z_]+)>>>`)

func signalText(sig string) string {
	return "<<<" + sig + ">>>"
}

// findSignal returns the first signal marker found in text, or "".
func findSignal(text string) string {
	m := signalRe.FindStringSubmatch(text)
	if m == nil {
		return ""
	}
	return "PLANNER:" + m[1]
}

// HasSignal reports whether text contains a signal marker.
func HasSignal(text, sig string) bool {
	return strings.Contains(text, signalText(sig))
}
