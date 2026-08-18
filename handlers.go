package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"time"
)

// Response caps. The frontend does its own escaping, but the server bounds
// payload sizes so a noisy cluster cannot blow up the browser.
const (
	maxStringLen   = 500
	maxWorkloads   = 15
	maxEvents      = 50
	maxLogs        = 50
	maxIssues      = 50
	restartsWindow = "1h"
)

// server wires configuration, the groundcover client, the response cache, and
// the sample-data fallback behind the HTTP handlers.
type server struct {
	cfg    Config
	gc     *GCClient
	cache  *Cache
	log    *slog.Logger
}

// newServer constructs a server. The sampler must already be parsed so a broken
// snapshot fails at boot rather than on first request.
func newServer(cfg Config, logger *slog.Logger) *server {
	return &server{
		cfg:    cfg,
		gc:     NewGCClient(cfg),
		cache:  NewCache(),
		log:    logger,
	}
}

// routes registers every /api/gc/* endpoint on the given mux.
func (s *server) routes(mux *http.ServeMux) {
	mux.HandleFunc("/api/gc/mode", s.handleMode)
	mux.HandleFunc("/api/gc/summary", s.handleSummary)
	mux.HandleFunc("/api/gc/series", s.handleSeries)
	mux.HandleFunc("/api/gc/workloads", s.handleWorkloads)
	mux.HandleFunc("/api/gc/zone-workloads", s.handleZoneWorkloads)
	mux.HandleFunc("/api/gc/events", s.handleEvents)
	mux.HandleFunc("/api/gc/logs", s.handleLogs)
	mux.HandleFunc("/api/gc/issues", s.handleIssues)
}

// ---------------------------------------------------------------------------
// HTTP helpers
// ---------------------------------------------------------------------------

// writeJSON writes a pre-rendered JSON payload with no-store cache headers.
func writeJSON(w http.ResponseWriter, status int, payload []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(payload)
}

// writeError renders a consistent error envelope. Internal error detail is
// logged by the caller, never leaked to the client.
func writeError(w http.ResponseWriter, status int, code, message string) {
	payload, _ := json.Marshal(map[string]string{"code": code, "message": message})
	writeJSON(w, status, payload)
}

// render is the shared live→cache pipeline for the section-style endpoints.
// It caches the rendered payload for cacheTTL and de-duplicates concurrent
// misses. There is NO sample fallback by design: no data beats fake data —
// an unconfigured or failing upstream answers with an honest error and the
// frontend renders an honest empty state.
func (s *server) render(w http.ResponseWriter, r *http.Request, cacheKey, endpoint string, _ map[string]any, live func(context.Context) (any, error)) {
	if !s.cfg.Live() {
		writeError(w, http.StatusServiceUnavailable, "not_configured", "groundcover is not configured — set GROUNDCOVER_API_KEY")
		return
	}
	payload, err := s.cache.Do(r.Context(), cacheKey, func(ctx context.Context) ([]byte, error) {
		v, liveErr := live(ctx)
		if liveErr != nil {
			return nil, liveErr
		}
		return json.Marshal(v)
	})
	if err != nil {
		s.log.Error("live fetch failed", "endpoint", endpoint, "error", err)
		writeError(w, http.StatusBadGateway, "upstream_failed", "groundcover query failed for "+endpoint)
		return
	}
	writeJSON(w, http.StatusOK, payload)
}

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

// handleMode reports whether the server is live and which cluster it targets.
// It reflects live server state directly (not the snapshot), so the frontend
// can decide between live and sample presentation.
func (s *server) handleMode(w http.ResponseWriter, r *http.Request) {
	payload, _ := json.Marshal(ModeResponse{
		Live:      s.cfg.Live(),
		Cluster:   s.cfg.Cluster,
		UIBaseURL: s.cfg.GroundcoverUIURL,
	})
	writeJSON(w, http.StatusOK, payload)
}

// handleSummary returns the cluster health summary.
func (s *server) handleSummary(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "summary", "summary", nil, s.buildSummary)
}

// handleSeries returns a time series for a metric over a range.
func (s *server) handleSeries(w http.ResponseWriter, r *http.Request) {
	metric := r.URL.Query().Get("metric")
	rng := r.URL.Query().Get("range")
	if rng == "" {
		rng = "1h"
	}
	if !validRange(rng) {
		writeError(w, http.StatusBadRequest, "invalid_range", "range must be 1h, 6h, or 24h")
		return
	}
	if metric != "cpu" && metric != "memory" && metric != "network" {
		writeError(w, http.StatusBadRequest, "invalid_metric", "metric must be cpu, memory, or network")
		return
	}

	if !s.cfg.Live() {
		writeError(w, http.StatusServiceUnavailable, "not_configured", "groundcover is not configured — set GROUNDCOVER_API_KEY")
		return
	}
	cacheKey := "series:" + metric + ":" + rng
	payload, err := s.cache.Do(r.Context(), cacheKey, func(ctx context.Context) ([]byte, error) {
		v, liveErr := s.buildSeries(ctx, metric, rng)
		if liveErr != nil {
			return nil, liveErr
		}
		return json.Marshal(v)
	})
	if err != nil {
		s.log.Error("live fetch failed", "endpoint", "series", "error", err)
		writeError(w, http.StatusBadGateway, "upstream_failed", "groundcover query failed for series")
		return
	}
	writeJSON(w, http.StatusOK, payload)
}

// handleWorkloads returns the top workloads by request rate with golden signals.
func (s *server) handleWorkloads(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "workloads", "workloads", nil, s.buildWorkloads)
}

// handleZoneWorkloads returns measured workloads inside theme zone
// namespaces, across every cluster reporting to the backend — deliberately not
// pinned to GC_CLUSTER, because zone workloads run on shared-pool clusters that
// report (or will report) under their own cluster names.
func (s *server) handleZoneWorkloads(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "zone-workloads", "zoneWorkloads", nil, s.buildZoneWorkloads)
}

// handleEvents returns recent Kubernetes events (warnings/errors first).
func (s *server) handleEvents(w http.ResponseWriter, r *http.Request) {
	rng := rangeOr(r, "1h")
	s.render(w, r, "events:"+rng, "events", nil, func(ctx context.Context) (any, error) {
		return s.buildEvents(ctx, isoPeriod(rng))
	})
}

// handleLogs returns recent error/warn log lines.
func (s *server) handleLogs(w http.ResponseWriter, r *http.Request) {
	rng := rangeOr(r, "1h")
	s.render(w, r, "logs:"+rng, "logs", nil, func(ctx context.Context) (any, error) {
		return s.buildLogs(ctx, isoPeriod(rng))
	})
}

// handleIssues returns active monitor issues.
func (s *server) handleIssues(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "issues", "issues", nil, func(ctx context.Context) (any, error) {
		return s.buildIssues(ctx)
	})
}

// ---------------------------------------------------------------------------
// Live builders — PromQL
// ---------------------------------------------------------------------------

// sel returns the cluster label selector shared by every PromQL query.
func (s *server) sel() string {
	return `{cluster="` + s.cfg.Cluster + `"}`
}

// buildSummary assembles the cluster health summary from PromQL scalars, with
// best-effort MCP enrichment for pod phase breakdown. The PromQL scalars are
// required; if any fails the whole summary falls back to sample. The MCP
// enrichment (total/failed pods) is best-effort and degrades gracefully.
func (s *server) buildSummary(ctx context.Context) (any, error) {
	sel := s.sel()

	scalars := map[string]string{
		"nodes":       `count(groundcover_node_capacity_mem_bytes` + sel + `)`,
		"nodesReady":  `count(groundcover_node_rt_mem_working_set_bytes` + sel + `)`,
		"podsRunning": `count(count by (pod)(groundcover_container_memory_working_set_bytes` + sel + `))`,
		"restarts":    `sum(increase(groundcover_container_restart_count_total` + sel + `[` + restartsWindow + `]))`,
		"cpuUsed":     `sum(groundcover_container_cpu_usage_rate_millis` + sel + `)/1000`,
		"cpuReq":      `sum(groundcover_container_cpu_request_m_cpu` + sel + `)/1000`,
		"memUsed":     `sum(groundcover_container_memory_working_set_bytes` + sel + `)`,
		"memReq":      `sum(groundcover_container_memory_request_bytes` + sel + `)`,
	}
	vals := make(map[string]float64, len(scalars))
	for k, q := range scalars {
		v, err := s.gc.ScalarInstant(ctx, q)
		if err != nil {
			return nil, fmt.Errorf("summary scalar %q: %w", k, err)
		}
		vals[k] = v
	}

	running := int(vals["podsRunning"])
	// Best-effort pod totals and failures via MCP; failures here must not sink
	// the whole summary, so log and continue with derived defaults.
	total := running
	if t, err := s.podTotal(ctx); err != nil {
		s.log.Warn("pod total via entities failed", "error", err)
	} else if t > running {
		total = t
	}
	failed := 0
	if f, err := s.podFailed(ctx); err != nil {
		s.log.Warn("failed-pod count via events failed", "error", err)
	} else {
		failed = f
	}
	pending := total - running - failed
	if pending < 0 {
		pending = 0
	}

	return SummaryResponse{
		Cluster:          s.cfg.Cluster,
		TS:               time.Now().UTC().Format(time.RFC3339),
		Nodes:            CountReady{Count: int(vals["nodes"]), Ready: int(vals["nodesReady"])},
		Pods:             PodPhases{Running: running, Pending: pending, Failed: failed},
		RestartsLastHour: int(vals["restarts"]),
		CPU:              CoreUsage{UsedCores: round(vals["cpuUsed"], 3), RequestedCores: round(vals["cpuReq"], 3)},
		Memory:           ByteUsage{UsedBytes: vals["memUsed"], RequestedBytes: vals["memReq"]},
	}, nil
}

// buildSeries runs a range query (or two, for network) and shapes the matrix
// into the SeriesResponse contract.
func (s *server) buildSeries(ctx context.Context, metric, rng string) (any, error) {
	sel := s.sel()
	end := time.Now().UTC()
	start := end.Add(-rangeDuration(rng))
	step := stepFor(rng)
	rateWin := strconv.Itoa(int(step.Seconds())) + "s"

	resp := SeriesResponse{Metric: metric, Range: rng, Series: []Series{}}

	switch metric {
	case "cpu":
		series, err := s.rangeSeries(ctx, "cpu", `sum(groundcover_container_cpu_usage_rate_millis`+sel+`)/1000`, start, end, step)
		if err != nil {
			return nil, err
		}
		resp.Series = append(resp.Series, series)
	case "memory":
		series, err := s.rangeSeries(ctx, "memory", `sum(groundcover_container_memory_working_set_bytes`+sel+`)`, start, end, step)
		if err != nil {
			return nil, err
		}
		resp.Series = append(resp.Series, series)
	case "network":
		rx, err := s.rangeSeries(ctx, "rx", `sum(rate(groundcover_host_net_receive_bytes_total`+sel+`[`+rateWin+`]))`, start, end, step)
		if err != nil {
			return nil, err
		}
		tx, err := s.rangeSeries(ctx, "tx", `sum(rate(groundcover_host_net_transmit_bytes_total`+sel+`[`+rateWin+`]))`, start, end, step)
		if err != nil {
			return nil, err
		}
		resp.Series = append(resp.Series, rx, tx)
	}
	return resp, nil
}

// rangeSeries runs a single range query and returns its first matrix row as a
// named Series (empty points when the query returns nothing).
func (s *server) rangeSeries(ctx context.Context, name, promql string, start, end time.Time, step time.Duration) (Series, error) {
	res, err := s.gc.QueryRange(ctx, promql, start, end, step)
	if err != nil {
		return Series{}, err
	}
	series := Series{Name: name, Points: []SeriesPoint{}}
	if len(res) == 0 {
		return series, nil
	}
	for _, v := range res[0].Values {
		ts, tErr := v.Time()
		val, vErr := v.Float()
		if tErr != nil || vErr != nil {
			continue
		}
		series.Points = append(series.Points, SeriesPoint{float64(ts), val})
	}
	return series, nil
}

// buildWorkloads merges request rate, error rate, latency quantiles, and
// restarts per workload from parallel PromQL grouping queries, returning the
// top maxWorkloads by request rate.
func (s *server) buildWorkloads(ctx context.Context) (any, error) {
	sel := s.sel()

	// Request rate carries the namespace label we key the table on.
	rps, err := s.gc.QueryInstant(ctx, `sum by (workload, namespace) (rate(groundcover_resource_total_counter`+sel+`[5m]))`)
	if err != nil {
		return nil, fmt.Errorf("workloads rps: %w", err)
	}

	byName := make(map[string]*Workload)
	for _, r := range rps {
		name := r.Metric["workload"]
		if name == "" {
			continue
		}
		v, _ := r.Value.Float()
		w := &Workload{Name: name, Namespace: r.Metric["namespace"], RPS: round(v, 3)}
		byName[name] = w
	}

	// Enrich with the remaining signals; each is best-effort so one empty
	// metric does not blank the table.
	s.mergeByWorkload(ctx, byName, `sum by (workload) (rate(groundcover_resource_error_counter`+sel+`[5m]))`,
		func(w *Workload, v float64) {
			if w.RPS > 0 {
				w.ErrorRatePct = round(v/w.RPS*100, 2)
			}
		})
	s.mergeByWorkload(ctx, byName, `avg by (workload) (groundcover_resource_latency_seconds`+sel[:len(sel)-1]+`,quantile="0.5"})`,
		func(w *Workload, v float64) { w.P50Ms = round(v*1000, 1) })
	s.mergeByWorkload(ctx, byName, `avg by (workload) (groundcover_resource_latency_seconds`+sel[:len(sel)-1]+`,quantile="0.95"})`,
		func(w *Workload, v float64) { w.P95Ms = round(v*1000, 1) })
	s.mergeByWorkload(ctx, byName, `sum by (workload) (increase(groundcover_container_restart_count_total`+sel+`[`+restartsWindow+`]))`,
		func(w *Workload, v float64) { w.Restarts = int(v) })

	workloads := make([]Workload, 0, len(byName))
	for _, w := range byName {
		workloads = append(workloads, *w)
	}
	sort.Slice(workloads, func(i, j int) bool { return workloads[i].RPS > workloads[j].RPS })
	if len(workloads) > maxWorkloads {
		workloads = workloads[:maxWorkloads]
	}
	return WorkloadsResponse{Workloads: workloads}, nil
}

// zoneSel selects theme zone namespaces across ALL clusters.
const zoneSel = `{namespace=~"theme-.*"}`

// buildZoneWorkloads assembles per-workload usage and golden signals for every
// workload in a theme-* namespace, on any reporting cluster. The base set
// comes from container CPU (every running container reports it); traffic
// signals enrich it best-effort.
func (s *server) buildZoneWorkloads(ctx context.Context) (any, error) {
	base, err := s.gc.QueryInstant(ctx, `sum by (workload, namespace, cluster) (groundcover_container_cpu_usage_rate_millis`+zoneSel+`)/1000`)
	if err != nil {
		return nil, fmt.Errorf("zone workloads cpu: %w", err)
	}

	byKey := make(map[string]*ZoneWorkload)
	for _, r := range base {
		name := r.Metric["workload"]
		ns := r.Metric["namespace"]
		if name == "" || ns == "" {
			continue
		}
		v, _ := r.Value.Float()
		byKey[name+"/"+ns] = &ZoneWorkload{
			Name:      name,
			Namespace: ns,
			Zone:      zoneFromNamespace(ns),
			Cluster:   r.Metric["cluster"],
			CPUCores:  round(v, 3),
		}
	}

	s.mergeZone(ctx, byKey, `sum by (workload, namespace) (groundcover_container_memory_working_set_bytes`+zoneSel+`)`,
		func(w *ZoneWorkload, v float64) { w.MemBytes = v })
	s.mergeZone(ctx, byKey, `sum by (workload, namespace) (rate(groundcover_resource_total_counter`+zoneSel+`[5m]))`,
		func(w *ZoneWorkload, v float64) { w.RPS = round(v, 3) })
	s.mergeZone(ctx, byKey, `sum by (workload, namespace) (rate(groundcover_resource_error_counter`+zoneSel+`[5m]))`,
		func(w *ZoneWorkload, v float64) {
			if w.RPS > 0 {
				w.ErrorRatePct = round(v/w.RPS*100, 2)
			}
		})
	s.mergeZone(ctx, byKey, `avg by (workload, namespace) (groundcover_resource_latency_seconds{namespace=~"theme-.*",quantile="0.5"})`,
		func(w *ZoneWorkload, v float64) { w.P50Ms = round(v*1000, 1) })
	s.mergeZone(ctx, byKey, `avg by (workload, namespace) (groundcover_resource_latency_seconds{namespace=~"theme-.*",quantile="0.95"})`,
		func(w *ZoneWorkload, v float64) { w.P95Ms = round(v*1000, 1) })
	s.mergeZone(ctx, byKey, `sum by (workload, namespace) (increase(groundcover_container_restart_count_total`+zoneSel+`[`+restartsWindow+`]))`,
		func(w *ZoneWorkload, v float64) { w.Restarts = int(v) })

	workloads := make([]ZoneWorkload, 0, len(byKey))
	for _, w := range byKey {
		workloads = append(workloads, *w)
	}
	sort.Slice(workloads, func(i, j int) bool {
		if workloads[i].Zone != workloads[j].Zone {
			return workloads[i].Zone < workloads[j].Zone
		}
		return workloads[i].CPUCores > workloads[j].CPUCores
	})
	if len(workloads) > maxWorkloads*3 {
		workloads = workloads[:maxWorkloads*3]
	}
	return ZoneWorkloadsResponse{AgentCoverage: len(workloads) > 0, Workloads: workloads}, nil
}

// mergeZone applies a grouped instant query onto the workload/namespace map,
// best-effort like mergeByWorkload.
func (s *server) mergeZone(ctx context.Context, byKey map[string]*ZoneWorkload, promql string, apply func(*ZoneWorkload, float64)) {
	res, err := s.gc.QueryInstant(ctx, promql)
	if err != nil {
		s.log.Warn("zone workload signal query failed", "error", err)
		return
	}
	for _, r := range res {
		w, ok := byKey[r.Metric["workload"]+"/"+r.Metric["namespace"]]
		if !ok {
			continue
		}
		if v, err := r.Value.Float(); err == nil {
			apply(w, v)
		}
	}
}

// zoneFromNamespace parses the zone name from a theme namespace
// (theme-<org...>-<zone> → <zone>). Best-effort: the frontend treats it as
// a correlation hint, not truth.
func zoneFromNamespace(ns string) string {
	for i := len(ns) - 1; i >= 0; i-- {
		if ns[i] == '-' {
			return ns[i+1:]
		}
	}
	return ""
}

// mergeByWorkload runs a grouped instant query and applies apply() to the
// matching workload for each result row. Query errors are logged and skipped so
// the table degrades gracefully.
func (s *server) mergeByWorkload(ctx context.Context, byName map[string]*Workload, promql string, apply func(*Workload, float64)) {
	res, err := s.gc.QueryInstant(ctx, promql)
	if err != nil {
		s.log.Warn("workload signal query failed", "error", err)
		return
	}
	for _, r := range res {
		w, ok := byName[r.Metric["workload"]]
		if !ok {
			continue
		}
		if v, err := r.Value.Float(); err == nil {
			apply(w, v)
		}
	}
}

// ---------------------------------------------------------------------------
// Live builders — MCP
// ---------------------------------------------------------------------------

// clusterFilter is the gcQL leading predicate scoping every MCP query to the
// configured cluster.
func (s *server) clusterFilter() string {
	return "cluster:" + s.cfg.Cluster
}

// podTotal returns the count of live (non-deleted) pods via the entities API.
func (s *server) podTotal(ctx context.Context) (int, error) {
	var rows []struct {
		C json.Number `json:"c"`
	}
	// query_entities operates on live state and takes no time parameters.
	q := "kind:Pod " + s.clusterFilter() + " | stats count() as c | limit 1"
	if err := s.gc.CallTool(ctx, "query_entities", map[string]any{"query": q}, &rows); err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, nil
	}
	n, _ := rows[0].C.Int64()
	return int(n), nil
}

// podFailed returns the number of distinct pods with container-crash events in
// the last hour.
func (s *server) podFailed(ctx context.Context) (int, error) {
	var rows []struct {
		C json.Number `json:"c"`
	}
	q := s.clusterFilter() + " type:container_crash | stats count_uniq(podName) as c | limit 1"
	if err := s.gc.QueryGCQL(ctx, "query_events", q, "PT1H", &rows); err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, nil
	}
	n, _ := rows[0].C.Int64()
	return int(n), nil
}

// eventRow is the projection selected from query_events.
type eventRow struct {
	Time       string `json:"_time"`
	Type       string `json:"type"`
	Reason     string `json:"reason"`
	EntityName string `json:"entity_name"`
	Workload   string `json:"workload"`
	PodName    string `json:"podName"`
	Namespace  string `json:"namespace"`
	Message    string `json:"message"`
}

// buildEvents returns recent non-Normal Kubernetes events, newest first.
func (s *server) buildEvents(ctx context.Context, period string) (any, error) {
	q := s.clusterFilter() + " -type:Normal | fields _time, type, reason, entity_name, workload, podName, namespace, message | sort by (_time desc) | limit " + strconv.Itoa(maxEvents)
	var rows []eventRow
	if err := s.gc.QueryGCQL(ctx, "query_events", q, period, &rows); err != nil {
		return nil, err
	}
	events := make([]Event, 0, len(rows))
	for _, r := range rows {
		events = append(events, Event{
			TS:        r.Time,
			Type:      r.Type,
			Reason:    r.Reason,
			Entity:    firstNonEmpty(r.EntityName, r.Workload, r.PodName),
			Namespace: r.Namespace,
			Message:   cap500(r.Message),
		})
	}
	return EventsResponse{Events: events}, nil
}

// logRow is the projection selected from query_logs.
type logRow struct {
	Time      string `json:"_time"`
	Level     string `json:"level"`
	Workload  string `json:"workload"`
	Namespace string `json:"namespace"`
	Content   string `json:"content"`
}

// buildLogs returns recent error/warn log lines, newest first.
func (s *server) buildLogs(ctx context.Context, period string) (any, error) {
	q := s.clusterFilter() + " level:in(error,warn,warning) | fields _time, level, workload, namespace, content | sort by (_time desc) | limit " + strconv.Itoa(maxLogs)
	var rows []logRow
	if err := s.gc.QueryGCQL(ctx, "query_logs", q, period, &rows); err != nil {
		return nil, err
	}
	logs := make([]LogEntry, 0, len(rows))
	for _, r := range rows {
		logs = append(logs, LogEntry{
			TS:        r.Time,
			Level:     r.Level,
			Workload:  r.Workload,
			Namespace: r.Namespace,
			Body:      cap500(r.Content),
		})
	}
	return LogsResponse{Logs: logs}, nil
}

// buildIssues returns active monitor issues. The issue schema varies and this
// cluster typically has none firing, so rows are decoded loosely and mapped
// best-effort.
func (s *server) buildIssues(ctx context.Context) (any, error) {
	q := s.clusterFilter() + " | sort by (_time desc) | limit " + strconv.Itoa(maxIssues)
	var rows []map[string]any
	if err := s.gc.QueryGCQL(ctx, "query_issues", q, "PT24H", &rows); err != nil {
		return nil, err
	}
	issues := make([]Issue, 0, len(rows))
	for _, r := range rows {
		issues = append(issues, Issue{
			Severity:  cap500(pickString(r, "severity", "level", "priority")),
			Title:     cap500(pickString(r, "title", "name", "monitor_name", "issue_name", "message")),
			Entity:    cap500(pickString(r, "entity_name", "workload", "resource", "entity")),
			Namespace: pickString(r, "namespace"),
			Since:     pickString(r, "_time", "start_time", "created_at", "since"),
		})
	}
	return IssuesResponse{Issues: issues}, nil
}

// ---------------------------------------------------------------------------
// small helpers
// ---------------------------------------------------------------------------

func validRange(r string) bool { return r == "1h" || r == "6h" || r == "24h" }

func rangeOr(r *http.Request, def string) string {
	v := r.URL.Query().Get("range")
	if !validRange(v) {
		return def
	}
	return v
}

func rangeDuration(r string) time.Duration {
	switch r {
	case "6h":
		return 6 * time.Hour
	case "24h":
		return 24 * time.Hour
	default:
		return time.Hour
	}
}

func stepFor(r string) time.Duration {
	switch r {
	case "6h":
		return 5 * time.Minute
	case "24h":
		return 15 * time.Minute
	default:
		return time.Minute
	}
}

func isoPeriod(r string) string {
	switch r {
	case "6h":
		return "PT6H"
	case "24h":
		return "PT24H"
	default:
		return "PT1H"
	}
}

// round rounds f to n decimal places (for stable, compact JSON numbers).
func round(f float64, n int) float64 {
	p := math10(n)
	return float64(int64(f*p+sign(f)*0.5)) / p
}

func sign(f float64) float64 {
	if f < 0 {
		return -1
	}
	return 1
}

func math10(n int) float64 {
	p := 1.0
	for i := 0; i < n; i++ {
		p *= 10
	}
	return p
}

// cap500 truncates a string to maxStringLen runes, appending an ellipsis when
// truncated.
func cap500(s string) string {
	if len(s) <= maxStringLen {
		return s
	}
	runes := []rune(s)
	if len(runes) <= maxStringLen {
		return s
	}
	return string(runes[:maxStringLen]) + "…"
}

// firstNonEmpty returns the first non-empty argument, or "".
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// pickString returns the first key present in m whose value is a non-empty
// string.
func pickString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}
