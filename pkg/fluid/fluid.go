package fluid

import (
	"context"
	"net/http"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/paerx/fluid/internal/auth"
	"github.com/paerx/fluid/internal/server"
)

type Logger interface {
	Printf(format string, args ...any)
}

type Handle struct {
	Addr     string
	BasePath string

	app    *server.App
	cancel context.CancelFunc
}

func (h *Handle) Close() error {
	if h.cancel != nil {
		h.cancel()
	}
	if h.app != nil {
		return h.app.Close()
	}
	return nil
}

func (h *Handle) SnapshotNow() error {
	if h.app == nil {
		return nil
	}
	_, err := h.app.SnapshotNow()
	return err
}

type Fluid struct {
	opts options
}

type options struct {
	addr                string
	basePath            string
	samplingInterval    time.Duration
	snapshotInterval    time.Duration
	snapshotMinInterval time.Duration
	maxSnapshotSize     int
	allowNetworks       []string
	enableUI            bool
	auth                auth.Authenticator
	mux                 *http.ServeMux
	logger              Logger
}

type Option func(*options)

type Auth interface {
	applyAuth(*options)
}

type tokenAuth string

func Token(token string) Auth {
	return tokenAuth(token)
}

func (t tokenAuth) applyAuth(o *options) {
	o.auth = auth.NewToken(string(t))
}

type basicAuth struct {
	user string
	pass string
}

func Basic(user, pass string) Auth {
	return basicAuth{user: user, pass: pass}
}

func (b basicAuth) applyAuth(o *options) {
	o.auth = auth.NewBasic(b.user, b.pass)
}

func New(opts ...Option) *Fluid {
	o := defaultOptions()
	for _, opt := range opts {
		opt(&o)
	}
	return &Fluid{opts: o}
}

func Goroutine(opts ...Option) (*Handle, error) {
	return New(opts...).Goroutine()
}

func Mount(mux *http.ServeMux, opts ...Option) (*Handle, error) {
	opts = append(opts, WithMux(mux))
	return New(opts...).Goroutine()
}

func (f *Fluid) Goroutine(extra ...Option) (*Handle, error) {
	o := f.opts
	for _, opt := range extra {
		opt(&o)
	}
	cfg := server.Config{
		Addr:                o.addr,
		BasePath:            o.basePath,
		SamplingInterval:    o.samplingInterval,
		SnapshotInterval:    o.snapshotInterval,
		SnapshotMinInterval: o.snapshotMinInterval,
		MaxSnapshotSize:     o.maxSnapshotSize,
		EnableUI:            o.enableUI,
		AllowNetworks:       o.allowNetworks,
		Auth:                o.auth,
		Mux:                 o.mux,
		Meta: map[string]any{
			"service": map[string]any{
				"name": "fluid",
				"pid":  os.Getpid(),
				"host": hostname(),
			},
			"runtime": map[string]any{
				"goVersion":  runtime.Version(),
				"gomaxprocs": runtime.GOMAXPROCS(0),
				"numCPU":     runtime.NumCPU(),
			},
			"build": map[string]any{
				"version": "v0.1.0",
				"commit":  "dev",
				"time":    time.Now().UTC().Format(time.RFC3339),
			},
			"server": map[string]any{
				"basePath":           o.basePath,
				"samplingIntervalMs": o.samplingInterval.Milliseconds(),
				"sseEnabled":         true,
			},
			"ts": time.Now().Unix(),
		},
	}

	app, err := server.New(cfg)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	if err := app.Start(ctx); err != nil {
		cancel()
		return nil, err
	}
	return &Handle{
		Addr:     o.addr,
		BasePath: o.basePath,
		app:      app,
		cancel:   cancel,
	}, nil
}

func defaultOptions() options {
	return options{
		addr:                "127.0.0.1:6068",
		basePath:            "/fluid",
		samplingInterval:    time.Second,
		snapshotMinInterval: 5 * time.Second,
		maxSnapshotSize:     200000,
		enableUI:            true,
		auth:                auth.Noop(),
	}
}

func WithAddr(addr string) Option {
	return func(o *options) { o.addr = addr }
}

func WithBasePath(path string) Option {
	return func(o *options) { o.basePath = path }
}

func WithSamplingInterval(d time.Duration) Option {
	return func(o *options) { o.samplingInterval = d }
}

func WithSnapshotInterval(d time.Duration) Option {
	return func(o *options) { o.snapshotInterval = d }
}

func WithSnapshotMinInterval(d time.Duration) Option {
	return func(o *options) { o.snapshotMinInterval = d }
}

func WithMaxSnapshotSize(n int) Option {
	return func(o *options) { o.maxSnapshotSize = n }
}

func WithAllowNetworks(networks []string) Option {
	return func(o *options) { o.allowNetworks = append([]string(nil), networks...) }
}

func WithAuth(a Auth) Option {
	return func(o *options) {
		if a != nil {
			a.applyAuth(o)
		}
	}
}

func WithAuthToken(token string) Option {
	return WithAuth(Token(token))
}

func WithMux(mux *http.ServeMux) Option {
	return func(o *options) { o.mux = mux }
}

func WithLogger(logger Logger) Option {
	return func(o *options) { o.logger = logger }
}

func WithUI(enabled bool) Option {
	return func(o *options) { o.enableUI = enabled }
}

var hostnameOnce sync.Once
var cachedHostname string

func hostname() string {
	hostnameOnce.Do(func() {
		cachedHostname, _ = os.Hostname()
	})
	return cachedHostname
}
