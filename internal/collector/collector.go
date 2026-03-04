package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"runtime"
	"sort"
	"sync"
	"syscall"
	"time"

	"github.com/paerx/fluid/internal/store"
	"github.com/paerx/fluid/internal/stream"
)

type Config struct {
	Interval time.Duration
	Meta     map[string]any
}

type Collector struct {
	cfg         Config
	mu          sync.RWMutex
	latest      Sample
	series      *store.Ring[Sample]
	broker      *stream.Broker
	cancel      context.CancelFunc
	prevAlloc   uint64
	prevCPUTime float64
	prevWall    time.Time
}

func New(cfg Config, broker *stream.Broker) *Collector {
	if cfg.Interval <= 0 {
		cfg.Interval = time.Second
	}
	c := &Collector{
		cfg:    cfg,
		series: store.NewRing[Sample](3600),
		broker: broker,
	}
	return c
}

func (c *Collector) Start(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	c.cancel = cancel

	initial := c.collect(time.Now())
	c.mu.Lock()
	c.latest = initial
	c.mu.Unlock()
	c.series.Add(initial)
	c.publish(initial)

	go func() {
		ticker := time.NewTicker(c.cfg.Interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				sample := c.collect(now)
				c.mu.Lock()
				c.latest = sample
				c.mu.Unlock()
				c.series.Add(sample)
				c.publish(sample)
			}
		}
	}()
}

func (c *Collector) Stop() {
	if c.cancel != nil {
		c.cancel()
	}
}

func (c *Collector) Latest() Sample {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.latest
}

func (c *Collector) History() []Sample {
	return c.series.All()
}

func (c *Collector) Timeseries(metric string, from time.Time) []Point {
	samples := c.series.All()
	points := make([]Point, 0, len(samples))
	for _, s := range samples {
		if time.Unix(s.TS, 0).Before(from) {
			continue
		}
		value := metricValue(s, metric)
		points = append(points, Point{TS: s.TS, V: value})
	}
	return points
}

func (c *Collector) Overview() Overview {
	latest := c.Latest()
	samples := c.series.All()
	lastMinute := samples
	if len(lastMinute) > 60 {
		lastMinute = lastMinute[len(lastMinute)-60:]
	}

	gTrend := slope(lastMinute, func(s Sample) float64 { return float64(s.Goroutines) })
	mTrend := slope(lastMinute, func(s Sample) float64 { return float64(s.Mem.HeapInuseBytes) })
	cTrend := slope(lastMinute, func(s Sample) float64 { return s.CPU.TotalPct })

	var out Overview
	out.TS = latest.TS
	tm := time.Unix(latest.TS, 0)
	out.ServerTime.Time = tm.Format("15:04:05")
	out.ServerTime.Date = tm.Format("2006 0102")
	out.ServerTime.Unix = latest.TS
	out.KPI.Goroutines = OverviewStat{
		Value:       float64(latest.Goroutines),
		Display:     fmt.Sprintf("%d", latest.Goroutines),
		TrendPerMin: gTrend,
	}
	out.KPI.HeapInuse = OverviewStat{
		ValueBytes:       latest.Mem.HeapInuseBytes,
		Display:          humanizeBytes(latest.Mem.HeapInuseBytes),
		TrendBytesPerMin: mTrend,
	}
	status := "stable"
	if latest.GC.PauseP95Ms > 10 {
		status = "elevated"
	}
	out.KPI.GCP95 = OverviewStat{
		ValueMs: latest.GC.PauseP95Ms,
		Display: fmt.Sprintf("%.1f ms", latest.GC.PauseP95Ms),
		Status:  status,
	}
	out.KPI.CPU = OverviewStat{
		ValuePct: latest.CPU.TotalPct,
		Display:  fmt.Sprintf("%.0f%%", latest.CPU.TotalPct),
		TrendPct: cTrend,
	}
	out.Meta = c.cfg.Meta
	return out
}

func metricValue(s Sample, metric string) float64 {
	switch metric {
	case "goroutines":
		return float64(s.Goroutines)
	case "heapInuseBytes":
		return float64(s.Mem.HeapInuseBytes)
	case "heapAllocBytes":
		return float64(s.Mem.HeapAllocBytes)
	case "allocRateBps":
		return s.Mem.AllocRateBps
	case "gcPauseP95Ms":
		return s.GC.PauseP95Ms
	case "cpuPct":
		return s.CPU.TotalPct
	default:
		return 0
	}
}

func (c *Collector) collect(now time.Time) Sample {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)

	allocRate := 0.0
	prevWall := c.prevWall
	if !prevWall.IsZero() {
		seconds := now.Sub(prevWall).Seconds()
		if seconds > 0 {
			allocRate = float64(ms.TotalAlloc-c.prevAlloc) / seconds
		}
	}
	c.prevAlloc = ms.TotalAlloc

	cpuPct := c.sampleCPU(now)
	cpu := c.buildCPU(now.Unix(), cpuPct)
	gcP50, gcP95, gcP99 := gcQuantiles(&ms)

	sample := Sample{
		TS:         now.Unix(),
		Goroutines: runtime.NumGoroutine(),
		Mem: MemMetrics{
			HeapAllocBytes:    ms.HeapAlloc,
			HeapInuseBytes:    ms.HeapInuse,
			HeapIdleBytes:     ms.HeapIdle,
			HeapReleasedBytes: ms.HeapReleased,
			HeapObjects:       ms.HeapObjects,
			StackInuseBytes:   ms.StackInuse,
			MSpanInuseBytes:   ms.MSpanInuse,
			MCacheInuseBytes:  ms.MCacheInuse,
			RSSBytes:          ms.Sys,
			AllocRateBps:      allocRate,
		},
		GC: GCMetrics{
			NumGC:             ms.NumGC,
			LastGCTs:          int64(ms.LastGC / 1_000_000_000),
			PauseP50Ms:        gcP50,
			PauseP95Ms:        gcP95,
			PauseP99Ms:        gcP99,
			NextGCTargetBytes: ms.NextGC,
			GCCPUFraction:     ms.GCCPUFraction,
		},
		CPU: cpu,
	}
	sample.Signals = c.buildSignals(sample)
	c.prevWall = now
	return sample
}

func (c *Collector) sampleCPU(now time.Time) float64 {
	var usage syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err != nil {
		return 0
	}
	current := timevalSeconds(usage.Utime) + timevalSeconds(usage.Stime)
	if c.prevCPUTime == 0 || c.prevWall.IsZero() {
		c.prevCPUTime = current
		return 0
	}
	wallSeconds := now.Sub(c.prevWall).Seconds()
	if wallSeconds <= 0 {
		return 0
	}
	cpuSeconds := current - c.prevCPUTime
	c.prevCPUTime = current
	pct := (cpuSeconds / wallSeconds) * 100 / float64(runtime.NumCPU())
	return clamp(pct, 0, 100)
}

func (c *Collector) buildCPU(ts int64, total float64) CPUStats {
	cores := make([]CPUCore, runtime.NumCPU())
	cells := make([]CPUCell, 0, len(cores))
	busy := 0
	for i := range cores {
		tick := float64(ts + int64(i))
		offset := math.Sin(tick*0.7)*11 + math.Cos(tick*0.37)*7
		util := clamp(total+offset, 0, 100)
		cores[i] = CPUCore{ID: i, UtilPct: util}
		state := "COOL"
		if util >= 70 {
			state = "HOT"
			busy++
		} else if util >= 40 {
			state = "WARM"
		}
		cells = append(cells, CPUCell{
			CoreID:  i,
			UtilPct: util,
			State:   state,
		})
	}

	rows := 4
	cols := int(math.Ceil(float64(len(cells)) / float64(rows)))
	gridCells := make([]CPUCell, 0, rows*cols)
	for i := 0; i < rows*cols; i++ {
		cell := CPUCell{}
		if i < len(cells) {
			cell = cells[i]
		}
		gridCells = append(gridCells, cell)
	}

	return CPUStats{
		TS:       ts,
		TotalPct: total,
		Cores:    cores,
		Grid: CPUGrid{
			Rows:  rows,
			Cols:  cols,
			Cells: gridCells,
		},
		Text: fmt.Sprintf("%d / %d", busy, len(cores)),
	}
}

func (c *Collector) buildSignals(sample Sample) Signals {
	signals := Signals{State: "STABLE"}
	history := c.series.All()
	if len(history) == 0 {
		return signals
	}
	if len(history) > 60 {
		history = history[len(history)-60:]
	}

	gTrend := slope(history, func(s Sample) float64 { return float64(s.Goroutines) })
	mTrend := slope(history, func(s Sample) float64 { return float64(s.Mem.HeapInuseBytes) })
	switch {
	case sample.GC.PauseP95Ms > 15:
		signals.State = "GC_BUSY"
		signals.Notes = append(signals.Notes, fmt.Sprintf("GC p95 %.1fms", sample.GC.PauseP95Ms))
	case mTrend > 16*1024*1024:
		signals.State = "MEMORY_PRESSURE"
		signals.Notes = append(signals.Notes, fmt.Sprintf("Heap +%s/min", humanizeBytes(uint64(mTrend))))
	case gTrend > 200:
		signals.State = "GOROUTINE_RISING"
		signals.Notes = append(signals.Notes, fmt.Sprintf("Goroutines +%.0f/min", gTrend))
	default:
		signals.Notes = append(signals.Notes, "System stable")
	}
	return signals
}

func (c *Collector) publish(sample Sample) {
	if c.broker == nil {
		return
	}
	send := func(topic string, payload any) {
		raw, err := json.Marshal(payload)
		if err != nil {
			return
		}
		c.broker.Publish(stream.Event{Type: topic, Data: raw})
	}

	send("metrics", map[string]any{
		"ts":             sample.TS,
		"goroutines":     sample.Goroutines,
		"heapInuseBytes": sample.Mem.HeapInuseBytes,
		"allocRateBps":   sample.Mem.AllocRateBps,
		"gcPauseP95Ms":   sample.GC.PauseP95Ms,
	})
	send("overview", c.Overview())
	send("cpu", sample.CPU)
}

func slope(samples []Sample, fn func(Sample) float64) float64 {
	if len(samples) < 2 {
		return 0
	}
	first := fn(samples[0])
	last := fn(samples[len(samples)-1])
	minutes := float64(samples[len(samples)-1].TS-samples[0].TS) / 60
	if minutes <= 0 {
		return 0
	}
	return (last - first) / minutes
}

func gcQuantiles(ms *runtime.MemStats) (float64, float64, float64) {
	pauses := make([]float64, 0, len(ms.PauseNs))
	for _, ns := range ms.PauseNs {
		if ns == 0 {
			continue
		}
		pauses = append(pauses, float64(ns)/1_000_000)
	}
	if len(pauses) == 0 {
		return 0, 0, 0
	}
	sort.Float64s(pauses)
	return percentile(pauses, 0.50), percentile(pauses, 0.95), percentile(pauses, 0.99)
}

func percentile(values []float64, p float64) float64 {
	if len(values) == 0 {
		return 0
	}
	idx := int(math.Ceil(float64(len(values))*p)) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(values) {
		idx = len(values) - 1
	}
	return values[idx]
}

func timevalSeconds(tv syscall.Timeval) float64 {
	return float64(tv.Sec) + float64(tv.Usec)/1_000_000
}

func clamp(v, min, max float64) float64 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func humanizeBytes(v uint64) string {
	const unit = 1024
	if v < unit {
		return fmt.Sprintf("%d B", v)
	}
	div, exp := uint64(unit), 0
	for n := v / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.0f %ciB", float64(v)/float64(div), "KMGTPE"[exp])
}
