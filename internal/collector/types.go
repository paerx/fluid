package collector

type Point struct {
	TS int64   `json:"ts"`
	V  float64 `json:"v"`
}

type MemMetrics struct {
	HeapAllocBytes    uint64  `json:"heapAllocBytes"`
	HeapInuseBytes    uint64  `json:"heapInuseBytes"`
	HeapIdleBytes     uint64  `json:"heapIdleBytes"`
	HeapReleasedBytes uint64  `json:"heapReleasedBytes"`
	HeapObjects       uint64  `json:"heapObjects"`
	StackInuseBytes   uint64  `json:"stackInuseBytes"`
	MSpanInuseBytes   uint64  `json:"mspanInuseBytes"`
	MCacheInuseBytes  uint64  `json:"mcacheInuseBytes"`
	RSSBytes          uint64  `json:"rssBytes"`
	AllocRateBps      float64 `json:"allocRateBps"`
}

type GCMetrics struct {
	NumGC             uint32  `json:"numGC"`
	LastGCTs          int64   `json:"lastGCTs"`
	PauseP50Ms        float64 `json:"pauseP50Ms"`
	PauseP95Ms        float64 `json:"pauseP95Ms"`
	PauseP99Ms        float64 `json:"pauseP99Ms"`
	NextGCTargetBytes uint64  `json:"nextGCTargetBytes"`
	GCCPUFraction     float64 `json:"gccpuFraction"`
}

type Signals struct {
	State string   `json:"state"`
	Notes []string `json:"notes"`
}

type CPUCore struct {
	ID      int     `json:"id"`
	UtilPct float64 `json:"utilPct"`
}

type CPUCell struct {
	CoreID  int     `json:"coreId"`
	UtilPct float64 `json:"utilPct"`
	State   string  `json:"state"`
}

type CPUGrid struct {
	Rows  int       `json:"rows"`
	Cols  int       `json:"cols"`
	Cells []CPUCell `json:"cells"`
}

type CPUStats struct {
	TS       int64     `json:"ts"`
	TotalPct float64   `json:"totalPct"`
	Cores    []CPUCore `json:"cores"`
	Grid     CPUGrid   `json:"grid"`
	Text     string    `json:"text"`
}

type Sample struct {
	TS         int64      `json:"ts"`
	Goroutines int        `json:"goroutines"`
	Mem        MemMetrics `json:"mem"`
	GC         GCMetrics  `json:"gc"`
	Signals    Signals    `json:"signals"`
	CPU        CPUStats   `json:"cpu"`
}

type OverviewStat struct {
	Value            float64 `json:"value,omitempty"`
	ValueBytes       uint64  `json:"valueBytes,omitempty"`
	ValueMs          float64 `json:"valueMs,omitempty"`
	ValuePct         float64 `json:"valuePct,omitempty"`
	Display          string  `json:"display,omitempty"`
	TrendPerMin      float64 `json:"trendPerMin,omitempty"`
	TrendBytesPerMin float64 `json:"trendBytesPerMin,omitempty"`
	TrendPct         float64 `json:"trendPct,omitempty"`
	Status           string  `json:"status,omitempty"`
}

type Overview struct {
	TS         int64 `json:"ts"`
	ServerTime struct {
		Time string `json:"time"`
		Date string `json:"date"`
		Unix int64  `json:"unix"`
	} `json:"serverTime"`
	KPI struct {
		Goroutines OverviewStat `json:"goroutines"`
		HeapInuse  OverviewStat `json:"heapInuse"`
		GCP95      OverviewStat `json:"gcP95"`
		CPU        OverviewStat `json:"cpu"`
	} `json:"kpi"`
	Meta map[string]any `json:"meta"`
}
