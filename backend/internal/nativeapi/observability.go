package nativeapi

type AdminObservabilityConfig struct {
	TelemetryEnabled bool `json:"telemetry_enabled"`
	ThemeEnabled     bool `json:"theme_enabled"`
}
type TrafficObservationPolicy struct {
	Enabled        bool          `json:"enabled"`
	Classification DecisionTable `json:"classification"`
}
type AccountTrafficCounters struct {
	Started         int64 `json:"started"`
	Completed2xx    int64 `json:"completed_2xx"`
	Upstream429     int64 `json:"upstream_429"`
	Upstream5xx     int64 `json:"upstream_5xx"`
	Cancelled       int64 `json:"cancelled"`
	FailedOther     int64 `json:"failed_other"`
	PeakInFlight    int   `json:"peak_in_flight"`
	RequestsLast60s int   `json:"requests_last_60s"`
	ObservedSinceMs int64 `json:"observed_since_ms"`
}
type AccountTrafficSnapshot struct {
	AccountID int64                             `json:"account_id"`
	Protocols map[string]AccountTrafficCounters `json:"protocols"`
}
type AccountTrafficDisplay struct {
	Protocol string `json:"protocol"`
	AccountTrafficCounters
	Finished       int64    `json:"finished"`
	Unfinished     int64    `json:"unfinished"`
	CompletionRate *float64 `json:"completion_rate"`
}
