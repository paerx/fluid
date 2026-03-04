package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/paerx/fluid/internal/auth"
	"github.com/paerx/fluid/internal/collector"
	"github.com/paerx/fluid/internal/snapshot"
	"github.com/paerx/fluid/internal/stream"
	"github.com/paerx/fluid/internal/ui"
)

type Config struct {
	Addr                string
	BasePath            string
	SamplingInterval    time.Duration
	SnapshotInterval    time.Duration
	SnapshotMinInterval time.Duration
	MaxSnapshotSize     int
	EnableUI            bool
	AllowNetworks       []string
	Auth                auth.Authenticator
	Meta                map[string]any
	Mux                 *http.ServeMux
}

type App struct {
	cfg           Config
	server        *http.Server
	mux           *http.ServeMux
	broker        *stream.Broker
	collector     *collector.Collector
	lastSnapshot  *snapshot.Snapshot
	snapMu        sync.RWMutex
	lastTriggered time.Time
}

func New(cfg Config) (*App, error) {
	if cfg.Addr == "" {
		cfg.Addr = "127.0.0.1:6068"
	}
	if cfg.BasePath == "" {
		cfg.BasePath = "/fluid"
	}
	if cfg.SamplingInterval <= 0 {
		cfg.SamplingInterval = time.Second
	}
	if cfg.SnapshotMinInterval <= 0 {
		cfg.SnapshotMinInterval = 5 * time.Second
	}
	if cfg.MaxSnapshotSize <= 0 {
		cfg.MaxSnapshotSize = 200000
	}
	if cfg.Auth == nil {
		cfg.Auth = auth.Noop()
	}
	if err := validateSecurity(cfg); err != nil {
		return nil, err
	}

	mux := cfg.Mux
	if mux == nil {
		mux = http.NewServeMux()
	}

	broker := stream.NewBroker()
	app := &App{
		cfg:    cfg,
		mux:    mux,
		broker: broker,
		collector: collector.New(collector.Config{
			Interval: cfg.SamplingInterval,
			Meta:     cfg.Meta,
		}, broker),
	}
	app.routes()
	return app, nil
}

func (a *App) Start(ctx context.Context) error {
	a.collector.Start(ctx)
	if a.cfg.SnapshotInterval > 0 {
		go a.snapshotLoop(ctx)
	}
	if a.cfg.Mux != nil {
		return nil
	}
	a.server = &http.Server{
		Addr:    a.cfg.Addr,
		Handler: a.mux,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = a.server.Shutdown(shutdownCtx)
	}()
	go a.server.ListenAndServe()
	return nil
}

func (a *App) Close() error {
	a.collector.Stop()
	if a.server != nil {
		return a.server.Close()
	}
	return nil
}

func (a *App) SnapshotNow() (*snapshot.Snapshot, error) {
	a.snapMu.Lock()
	defer a.snapMu.Unlock()
	return a.captureSnapshotLocked(snapshot.Request{
		GroupBy:       "topFunction",
		TopN:          20,
		MaxGoroutines: a.cfg.MaxSnapshotSize,
		WithDelta:     true,
	})
}

func validateSecurity(cfg Config) error {
	host, _, err := net.SplitHostPort(cfg.Addr)
	if err != nil {
		return err
	}
	local := host == "127.0.0.1" || host == "localhost" || host == "::1"
	if !local && !cfg.Auth.Enabled() {
		return fmt.Errorf("fluid: auth is required when binding to %s", cfg.Addr)
	}
	return nil
}

func (a *App) routes() {
	base := strings.TrimRight(a.cfg.BasePath, "/")
	if base == "" {
		base = "/fluid"
	}

	api := base + "/api/v1"
	if a.cfg.EnableUI || a.cfg.Mux == nil {
		uiFS, _ := fs.Sub(ui.FS(), ".")
		fileServer := http.FileServer(http.FS(uiFS))
		a.mux.Handle(base+"/assets/", http.StripPrefix(base+"/", fileServer))
		a.mux.Handle(base+"/styles.css", http.StripPrefix(base+"/", fileServer))
		a.mux.Handle(base+"/app.js", http.StripPrefix(base+"/", fileServer))
		a.mux.HandleFunc(base, func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, base+"/", http.StatusTemporaryRedirect)
		})
		a.mux.HandleFunc(base+"/", func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, base+"/api/") {
				http.NotFound(w, r)
				return
			}
			relative := strings.TrimPrefix(r.URL.Path, base+"/")
			if relative != "" {
				if info, err := fs.Stat(uiFS, relative); err == nil && !info.IsDir() {
					http.StripPrefix(base+"/", fileServer).ServeHTTP(w, r)
					return
				}
			}
			http.ServeFileFS(w, r, uiFS, "index.html")
		})
	}

	a.mux.HandleFunc(api+"/health", a.handleAPI(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "ts": time.Now().Unix()})
	}))
	a.mux.HandleFunc(api+"/meta", a.handleAPI(a.handleMeta))
	a.mux.HandleFunc(api+"/metrics", a.handleAPI(a.handleMetrics))
	a.mux.HandleFunc(api+"/overview", a.handleAPI(a.handleOverview))
	a.mux.HandleFunc(api+"/timeseries", a.handleAPI(a.handleTimeseries))
	a.mux.HandleFunc(api+"/memory", a.handleAPI(a.handleMemory))
	a.mux.HandleFunc(api+"/cpu", a.handleAPI(a.handleCPU))
	a.mux.HandleFunc(api+"/snapshot/goroutines", a.handleAPI(a.handleSnapshot))
	a.mux.HandleFunc(api+"/goroutines/top", a.handleAPI(a.handleGoroutinesTop))
	a.mux.HandleFunc(api+"/goroutines/group", a.handleAPI(a.handleGoroutinesGroup))
	a.mux.HandleFunc(api+"/stream", a.handleAPI(a.handleStream))
}

func (a *App) handleAPI(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !a.isAllowed(r) {
			writeError(w, http.StatusForbidden, "FORBIDDEN", "request source is not allowed", nil)
			return
		}
		if a.cfg.Auth.Enabled() && !a.cfg.Auth.Authorize(r) {
			writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "invalid or missing token", nil)
			return
		}
		next(w, r)
	}
}

func (a *App) isAllowed(r *http.Request) bool {
	if len(a.cfg.AllowNetworks) == 0 {
		return true
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	for _, cidr := range a.cfg.AllowNetworks {
		_, network, err := net.ParseCIDR(cidr)
		if err == nil && network.Contains(ip) {
			return true
		}
	}
	return false
}

func (a *App) handleMeta(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.cfg.Meta)
}

func (a *App) handleMetrics(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.collector.Latest())
}

func (a *App) handleOverview(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.collector.Overview())
}

func (a *App) handleMemory(w http.ResponseWriter, r *http.Request) {
	latest := a.collector.Latest()
	writeJSON(w, http.StatusOK, map[string]any{
		"ts":  latest.TS,
		"mem": latest.Mem,
		"gc":  latest.GC,
	})
}

func (a *App) handleCPU(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.collector.Latest().CPU)
}

func (a *App) handleTimeseries(w http.ResponseWriter, r *http.Request) {
	window := parseWindow(r.URL.Query().Get("window"))
	metric := r.URL.Query().Get("metric")
	if metric == "" {
		metric = "goroutines"
	}
	points := a.collector.Timeseries(metric, time.Now().Add(-window))
	writeJSON(w, http.StatusOK, map[string]any{
		"metric": metric,
		"window": window.String(),
		"stepMs": int(a.cfg.SamplingInterval / time.Millisecond),
		"fromTs": time.Now().Add(-window).Unix(),
		"toTs":   time.Now().Unix(),
		"points": points,
	})
}

func (a *App) handleSnapshot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "BAD_REQUEST", "method not allowed", nil)
		return
	}
	if retry := time.Until(a.lastTriggered.Add(a.cfg.SnapshotMinInterval)); retry > 0 {
		writeError(w, http.StatusTooManyRequests, "TOO_MANY_REQUESTS", "snapshot throttled", map[string]any{
			"retryAfterMs": retry.Milliseconds(),
		})
		return
	}
	var req snapshot.Request
	_ = json.NewDecoder(r.Body).Decode(&req)
	req.MaxGoroutines = a.cfg.MaxSnapshotSize

	a.snapMu.Lock()
	defer a.snapMu.Unlock()
	snap, err := a.captureSnapshotLocked(req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"snapshotId":    snap.SnapshotID,
		"accepted":      true,
		"minIntervalMs": a.cfg.SnapshotMinInterval.Milliseconds(),
		"ts":            snap.SnapshotTS,
	})
}

func (a *App) captureSnapshotLocked(req snapshot.Request) (*snapshot.Snapshot, error) {
	snap, err := snapshot.Capture(req, a.lastSnapshot)
	if err != nil {
		return nil, err
	}
	a.lastTriggered = time.Now()
	a.lastSnapshot = snap
	raw, err := json.Marshal(snap)
	if err == nil {
		a.broker.Publish(stream.Event{Type: "snapshot", Data: raw})
	}
	return snap, nil
}

func (a *App) handleGoroutinesTop(w http.ResponseWriter, r *http.Request) {
	a.snapMu.RLock()
	defer a.snapMu.RUnlock()
	if a.lastSnapshot == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"snapshotId": "",
			"items":      []any{},
		})
		return
	}
	writeJSON(w, http.StatusOK, a.lastSnapshot)
}

func (a *App) handleGoroutinesGroup(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "missing id", nil)
		return
	}
	a.snapMu.RLock()
	defer a.snapMu.RUnlock()
	item := snapshot.FindItem(a.lastSnapshot, id)
	if item == nil {
		writeError(w, http.StatusNotFound, "BAD_REQUEST", "group not found", nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"snapshotId":     a.lastSnapshot.SnapshotID,
		"id":             item.ID,
		"name":           item.Name,
		"count":          item.Count,
		"stateTop":       item.StateTop,
		"stateHistogram": item.StateHistogram,
		"topFrames":      item.TopFrames,
		"stack":          item.Stack,
		"rawSample":      item.RawSample,
	})
}

func (a *App) handleStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "streaming unsupported", nil)
		return
	}

	topics := map[string]bool{}
	for _, topic := range strings.Split(r.URL.Query().Get("topics"), ",") {
		topic = strings.TrimSpace(topic)
		if topic != "" {
			topics[topic] = true
		}
	}
	if len(topics) == 0 {
		topics["overview"] = true
		topics["metrics"] = true
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	id, ch := a.broker.Subscribe()
	defer a.broker.Unsubscribe(id)

	fmt.Fprint(w, ": ok\n\n")
	flusher.Flush()

	notify := r.Context().Done()
	for {
		select {
		case <-notify:
			return
		case evt := <-ch:
			if !topics[evt.Type] {
				continue
			}
			fmt.Fprintf(w, "event: %s\n", evt.Type)
			fmt.Fprintf(w, "data: %s\n\n", evt.Data)
			flusher.Flush()
		}
	}
}

func (a *App) snapshotLoop(ctx context.Context) {
	ticker := time.NewTicker(a.cfg.SnapshotInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.snapMu.Lock()
			_, _ = a.captureSnapshotLocked(snapshot.Request{
				GroupBy:       "topFunction",
				TopN:          20,
				MaxGoroutines: a.cfg.MaxSnapshotSize,
				WithDelta:     true,
			})
			a.snapMu.Unlock()
		}
	}
}

func parseWindow(raw string) time.Duration {
	switch raw {
	case "1m":
		return time.Minute
	case "5m":
		return 5 * time.Minute
	case "1h":
		return time.Hour
	case "6h":
		return 6 * time.Hour
	default:
		return 15 * time.Minute
	}
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, code, message string, details map[string]any) {
	writeJSON(w, status, map[string]any{
		"error": map[string]any{
			"code":      code,
			"message":   message,
			"requestId": strconv.FormatInt(time.Now().UnixNano(), 36),
			"details":   details,
		},
	})
}
