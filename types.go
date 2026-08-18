package main

// The types below are the on-the-wire API contract for /api/gc/*. The frontend
// depends on these exact JSON shapes; do not rename fields without updating the
// static/ frontend workstream.

// ModeResponse is returned by GET /api/gc/mode.
type ModeResponse struct {
	Live      bool   `json:"live"`
	Cluster   string `json:"cluster"`
	UIBaseURL string `json:"uiBaseUrl"`
}

// CountReady is a count paired with how many are ready (nodes).
type CountReady struct {
	Count int `json:"count"`
	Ready int `json:"ready"`
}

// PodPhases holds a pod count broken down by lifecycle phase.
type PodPhases struct {
	Running int `json:"running"`
	Pending int `json:"pending"`
	Failed  int `json:"failed"`
}

// CoreUsage pairs used vs requested CPU cores.
type CoreUsage struct {
	UsedCores      float64 `json:"usedCores"`
	RequestedCores float64 `json:"requestedCores"`
}

// ByteUsage pairs used vs requested memory bytes.
type ByteUsage struct {
	UsedBytes      float64 `json:"usedBytes"`
	RequestedBytes float64 `json:"requestedBytes"`
}

// SummaryResponse is returned by GET /api/gc/summary.
type SummaryResponse struct {
	Cluster          string     `json:"cluster"`
	TS               string     `json:"ts"`
	Nodes            CountReady `json:"nodes"`
	Pods             PodPhases  `json:"pods"`
	RestartsLastHour int        `json:"restartsLastHour"`
	CPU              CoreUsage  `json:"cpu"`
	Memory           ByteUsage  `json:"memory"`
}

// SeriesPoint is a single [unixSeconds, value] chart sample.
type SeriesPoint [2]float64

// Series is one named line on a chart.
type Series struct {
	Name   string        `json:"name"`
	Points []SeriesPoint `json:"points"`
}

// SeriesResponse is returned by GET /api/gc/series.
type SeriesResponse struct {
	Metric string   `json:"metric"`
	Range  string   `json:"range"`
	Series []Series `json:"series"`
}

// Workload is one row of the golden-signals table.
type Workload struct {
	Name         string  `json:"name"`
	Namespace    string  `json:"namespace"`
	Kind         string  `json:"kind"`
	RPS          float64 `json:"rps"`
	ErrorRatePct float64 `json:"errorRatePct"`
	P50Ms        float64 `json:"p50Ms"`
	P95Ms        float64 `json:"p95Ms"`
	Restarts     int     `json:"restarts"`
}

// WorkloadsResponse is returned by GET /api/gc/workloads.
type WorkloadsResponse struct {
	Workloads []Workload `json:"workloads"`
}

// Event is one Kubernetes event row.
type Event struct {
	TS        string `json:"ts"`
	Type      string `json:"type"`
	Reason    string `json:"reason"`
	Entity    string `json:"entity"`
	Namespace string `json:"namespace"`
	Message   string `json:"message"`
}

// EventsResponse is returned by GET /api/gc/events.
type EventsResponse struct {
	Events []Event `json:"events"`
}

// LogEntry is one log line row.
type LogEntry struct {
	TS        string `json:"ts"`
	Level     string `json:"level"`
	Workload  string `json:"workload"`
	Namespace string `json:"namespace"`
	Body      string `json:"body"`
}

// LogsResponse is returned by GET /api/gc/logs.
type LogsResponse struct {
	Logs []LogEntry `json:"logs"`
}

// ZoneWorkload is one measured workload running in a theme zone namespace.
// Zone is parsed from the namespace suffix (theme-<org...>-<zone>); the
// frontend correlates it against the theme zone list.
type ZoneWorkload struct {
	Name         string  `json:"name"`
	Namespace    string  `json:"namespace"`
	Zone         string  `json:"zone"`
	Cluster      string  `json:"cluster"`
	CPUCores     float64 `json:"cpuCores"`
	MemBytes     float64 `json:"memBytes"`
	RPS          float64 `json:"rps"`
	ErrorRatePct float64 `json:"errorRatePct"`
	P50Ms        float64 `json:"p50Ms"`
	P95Ms        float64 `json:"p95Ms"`
	Restarts     int     `json:"restarts"`
}

// ZoneWorkloadsResponse is returned by GET /api/gc/zone-workloads.
// AgentCoverage is false when no theme-* namespace reports metrics — i.e.
// the zone workload clusters have no groundcover agent yet.
type ZoneWorkloadsResponse struct {
	AgentCoverage bool           `json:"agentCoverage"`
	Workloads     []ZoneWorkload `json:"workloads"`
}

// Issue is one monitor-issue row.
type Issue struct {
	Severity  string `json:"severity"`
	Title     string `json:"title"`
	Entity    string `json:"entity"`
	Namespace string `json:"namespace"`
	Since     string `json:"since"`
}

// IssuesResponse is returned by GET /api/gc/issues.
type IssuesResponse struct {
	Issues []Issue `json:"issues"`
}
