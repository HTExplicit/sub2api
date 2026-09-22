package policy

import (
	"testing"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

func TestObservationRulesKeepCancellationAboveErrorsAndRequireWSTerminals(t *testing.T) {
	for _, tc := range []struct {
		facts map[string]string
		want  string
	}{
		{map[string]string{"client_cancelled": "true", "has_error": "true", "error_status": "503"}, "cancelled"},
		{map[string]string{"ws": "true", "terminal": "response.incomplete", "has_error": "true", "error_status": "429"}, "cancelled"},
		{map[string]string{"ws": "true", "has_result": "true", "has_error": "false", "terminal": ""}, "failed_other"},
		{map[string]string{"ws": "true", "has_result": "true", "has_error": "false", "terminal": "response.failed", "terminal_status": "503"}, "upstream_5xx"},
		{map[string]string{"ws": "false", "has_result": "true", "has_error": "false"}, "completed_2xx"},
	} {
		got, err := trafficRules().Evaluate(tc.facts)
		if err != nil || got != tc.want {
			t.Fatalf("classification=%s, want=%s err=%v", got, tc.want, err)
		}
	}
}

func TestObservationDisplayDoesNotTreatOpenTurnsAsFailuresOrMissingDataAsSuccess(t *testing.T) {
	rows := projectTraffic(extensionv1.AccountTrafficSnapshot{Protocols: map[string]extensionv1.AccountTrafficCounters{
		"http": {Started: 10, Completed2xx: 4, Upstream429: 1}, "ws": {},
	}})
	if len(rows) != 2 || rows[0].Finished != 5 || rows[0].Unfinished != 5 || rows[0].CompletionRate == nil || *rows[0].CompletionRate != .8 || rows[1].CompletionRate != nil {
		t.Fatalf("invalid observation display: %+v", rows)
	}
}
