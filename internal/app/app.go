// Package app wires all PipeRouter components together: configuration
// manager, proxy and admin HTTP servers, hot reload, logging and graceful
// shutdown (PRD §4, §21, §22.3).
package app

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/kites262/piperouter/internal/api"
	"github.com/kites262/piperouter/internal/config"
	"github.com/kites262/piperouter/internal/logging"
	"github.com/kites262/piperouter/internal/metrics"
	"github.com/kites262/piperouter/internal/proxy"
	"github.com/kites262/piperouter/internal/runtime"
	"github.com/kites262/piperouter/internal/webui"
)

// Shutdown budgets (PRD §22.3): the proxy drains long-running streams for
// up to 30s, the admin plane closes within 10s.
const (
	proxyShutdownBudget = 30 * time.Second
	adminShutdownBudget = 10 * time.Second
)

// Options are the CLI-provided runtime overrides (PRD §21.3). They take
// precedence over the configuration file but are never written back to it.
type Options struct {
	ConfigPath   string // default "piperouter.yaml"
	ProxyListen  string // CLI override, "" = use config
	AdminListen  string // CLI override, "" = use config
	DisableAdmin bool   // force admin (and therefore web) off
	DisableWeb   bool   // force web off
	LogLevel     string // CLI override, "" = use config
	Version      string
}

// Run starts the application and blocks until ctx is done or SIGINT/SIGTERM
// arrives, then shuts down gracefully (PRD §22.3). A second signal forces an
// immediate close of both servers.
func Run(ctx context.Context, opts Options) error {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	a, err := Start(ctx, opts)
	if err != nil {
		return err
	}

	select {
	case <-ctx.Done():
		a.logger.Info("shutdown requested, draining", "proxy_budget", proxyShutdownBudget.String())
	case err := <-a.serveErr:
		a.logger.Error("server failed", "error", err.Error())
		_ = a.Shutdown(context.Background())
		return err
	}
	// Release the NotifyContext registration so a second signal reaches the
	// force-close watcher below instead of being swallowed.
	stop()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sig)
	done := make(chan struct{})
	go func() {
		select {
		case <-sig:
			a.logger.Warn("second signal received, forcing immediate shutdown")
			a.Close()
		case <-done:
		}
	}()

	err = a.Shutdown(context.Background())
	close(done)
	return err
}

// App is a started PipeRouter instance. It is an allowed contract extension
// beyond Run(ctx, opts), exported for the app package's own tests and for
// integration tests that need the bound listener addresses (":0" ports).
type App struct {
	logger   *slog.Logger
	levelVar *slog.LevelVar
	manager  *runtime.Manager
	registry *metrics.Registry
	ring     *logging.Ring
	watcher  *config.Watcher // nil when hot reload is unavailable

	proxySrv     *http.Server
	proxyHandler proxy.Handler // for WebSocket tunnel draining at shutdown
	adminSrv     *http.Server  // nil when the admin plane is disabled

	proxyAddr string
	adminAddr string // "" when the admin plane is disabled

	serveErr chan error // fatal Serve errors (never http.ErrServerClosed)
	wg       sync.WaitGroup
	shutOnce sync.Once
	shutErr  error
}

// ProxyAddr returns the bound proxy listener address (host:port).
func (a *App) ProxyAddr() string { return a.proxyAddr }

// AdminAddr returns the bound admin listener address, or "" when the admin
// plane is disabled.
func (a *App) AdminAddr() string { return a.adminAddr }

// Start binds the listeners, launches the servers and returns immediately.
// Used by Run and directly by tests; callers must call Shutdown or Close.
func Start(ctx context.Context, opts Options) (*App, error) {
	configPath := opts.ConfigPath
	if configPath == "" {
		configPath = "piperouter.yaml"
	}
	if opts.Version == "" {
		opts.Version = "dev"
	}

	// Bootstrap logger at info so manager/startup problems are visible even
	// before the configured level is known; the same LevelVar is retuned to
	// the effective level below and on every config swap.
	logger, levelVar := logging.New(slog.LevelInfo)

	reg := metrics.NewRegistry()
	manager, err := runtime.NewManager(configPath, logger, reg)
	if err != nil {
		return nil, fmt.Errorf("invalid configuration %q: %w (fix the file or run `piperouter validate --config %s`)", configPath, err, configPath)
	}
	snap := manager.Current()
	cfg := snap.Config

	// Effective settings: config overridden by CLI (§21.3). Overrides are
	// runtime-only and never written back to the file.
	proxyListen := cfg.Server.Proxy.Listen
	if opts.ProxyListen != "" {
		proxyListen = opts.ProxyListen
	}
	adminListen := cfg.Server.Admin.Listen
	if opts.AdminListen != "" {
		adminListen = opts.AdminListen
	}
	adminEnabled := cfg.Server.Admin.IsEnabled() && !opts.DisableAdmin
	// WebUI is served by the admin server, so web is forced off whenever
	// admin is off (PRD §4.3).
	webEnabled := cfg.Server.Web.IsEnabled() && !opts.DisableWeb && adminEnabled

	levelName := cfg.Runtime.LogLevel
	if opts.LogLevel != "" {
		levelName = opts.LogLevel
	}
	level, err := logging.ParseLevel(levelName)
	if err != nil {
		return nil, fmt.Errorf("--log-level: %w", err)
	}
	levelVar.Set(level)

	a := &App{
		logger:   logger,
		levelVar: levelVar,
		manager:  manager,
		registry: reg,
		ring:     logging.NewRing(*cfg.Runtime.RecentLogs),
		serveErr: make(chan error, 2),
	}

	// Hot-apply runtime settings on every successful config swap. OnSwap
	// callbacks run under the manager's mutation lock: they must be fast
	// and must never call Apply/ReloadFromFile (deadlock).
	cliLogLevel := opts.LogLevel
	manager.OnSwap(func(s *runtime.Snapshot) {
		if s.Config.Runtime.RecentLogs != nil {
			a.ring.SetCapacity(*s.Config.Runtime.RecentLogs)
		}
		if cliLogLevel != "" {
			return // CLI override outranks the file for the whole process lifetime
		}
		if lv, err := logging.ParseLevel(s.Config.Runtime.LogLevel); err == nil {
			a.levelVar.Set(lv)
		}
	})

	// Proxy listener/server. No Read/WriteTimeout: uploads, SSE and
	// WebSocket streams may legitimately last hours (PRD §10.3).
	tlsCfg := cfg.Server.Proxy.TLS
	var proxyTLS *tls.Config
	if tlsCfg.Enabled {
		cert, err := tls.LoadX509KeyPair(tlsCfg.CertFile, tlsCfg.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("load TLS certificate: %w", err)
		}
		proxyTLS = &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
		}
	}

	// slog bridge for net/http server noise (TLS scans, bad handshakes):
	// debug level so it never floods production logs.
	httpErrLog := slog.NewLogLogger(logger.Handler(), slog.LevelDebug)

	a.proxyHandler = proxy.NewHandler(manager, reg, a.ring, logger)
	a.proxySrv = &http.Server{
		Handler:     a.proxyHandler,
		IdleTimeout: 120 * time.Second,
		TLSConfig:   proxyTLS,
		ErrorLog:    httpErrLog,
	}

	// Bind listeners before starting goroutines so ":0" test ports resolve
	// and bind errors surface immediately (PRD §22.6).
	lc := &net.ListenConfig{}
	proxyLn, err := lc.Listen(ctx, "tcp", proxyListen)
	if err != nil {
		return nil, fmt.Errorf("bind proxy listener %q: %w", proxyListen, err)
	}
	a.proxyAddr = proxyLn.Addr().String()

	var adminLn net.Listener
	if adminEnabled {
		// Bind first so the handler can report the EFFECTIVE bound address
		// (which honors CLI overrides and ":0" test ports) in /status (§21.3).
		adminLn, err = lc.Listen(ctx, "tcp", adminListen)
		if err != nil {
			proxyLn.Close()
			return nil, fmt.Errorf("bind admin listener %q: %w", adminListen, err)
		}
		a.adminAddr = adminLn.Addr().String()

		var webHandler http.Handler
		if webEnabled && webui.Available() {
			webHandler = webui.Handler()
		}
		a.adminSrv = &http.Server{
			Handler: api.NewHandler(api.Deps{
				Manager:   manager,
				Metrics:   reg,
				Ring:      a.ring,
				Logger:    logger,
				Version:   opts.Version,
				WebUI:     webHandler,
				ProxyAddr: a.proxyAddr,
				AdminAddr: a.adminAddr,
				// Loopback-bound admin (the default) enforces loopback Host
				// to block DNS rebinding; a deliberately exposed admin plane
				// sits behind a proxy that forwards arbitrary Host (§15.3).
				RestrictHost: isLoopbackListen(adminListen),
			}),
			ReadTimeout:  10 * time.Second,
			WriteTimeout: 60 * time.Second,
			IdleTimeout:  120 * time.Second,
			ErrorLog:     httpErrLog,
		}
		if !isLoopbackListen(adminListen) {
			logger.Warn("SECURITY WARNING: the admin API has no authentication and is listening on a NON-LOOPBACK address — " +
				"anyone who can reach it can read and rewrite the entire configuration. " +
				"Bind it to 127.0.0.1 or put an authenticating reverse proxy (or firewall) in front of it (PRD §15.3).")
		}
	}

	// Hot reload watcher: failure is not fatal — the process stays fully
	// functional, it just requires a restart (or an Admin API write) to
	// pick up external file edits.
	watcher, err := config.NewWatcher(configPath, logger, func() {
		// Reload errors are already logged and recorded by the manager.
		_ = manager.ReloadFromFile()
	})
	if err != nil {
		logger.Warn("config file watching unavailable, hot reload disabled", "error", err.Error())
	} else {
		a.watcher = watcher
	}

	// Startup summary (PRD §4.2).
	logger.Info("piperouter starting",
		"version", opts.Version,
		"config", configPath,
		"revision", snap.Revision,
	)
	logger.Info("proxy server listening", "addr", a.proxyAddr, "tls", tlsCfg.Enabled)
	if adminEnabled {
		logger.Info("admin server listening", "addr", a.adminAddr, "webui", webEnabled && webui.Available())
	} else {
		logger.Info("admin server disabled", "webui", false)
	}

	a.serve(a.proxySrv, proxyLn, tlsCfg.Enabled)
	if a.adminSrv != nil {
		a.serve(a.adminSrv, adminLn, false)
	}
	return a, nil
}

// serve runs srv on ln in a goroutine, reporting fatal errors on serveErr.
func (a *App) serve(srv *http.Server, ln net.Listener, useTLS bool) {
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		var err error
		if useTLS {
			// Certificates come from srv.TLSConfig; ServeTLS also arranges
			// the h2 NextProtos.
			err = srv.ServeTLS(ln, "", "")
		} else {
			err = srv.Serve(ln)
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			select {
			case a.serveErr <- err:
			default:
			}
		}
	}()
}

// Shutdown gracefully stops both servers concurrently — proxy with a 30s
// drain budget, admin with 10s (both additionally capped by ctx) — closes
// the config watcher and waits for the serve goroutines (PRD §22.3). It is
// idempotent.
func (a *App) Shutdown(ctx context.Context) error {
	a.shutOnce.Do(func() {
		var mu sync.Mutex
		var firstErr error
		record := func(err error) {
			mu.Lock()
			defer mu.Unlock()
			if firstErr == nil && err != nil {
				firstErr = err
			}
		}

		var wg sync.WaitGroup
		stop := func(srv *http.Server, budget time.Duration) {
			defer wg.Done()
			c, cancel := context.WithTimeout(ctx, budget)
			defer cancel()
			if err := srv.Shutdown(c); err != nil {
				// Budget exceeded: force-close lingering connections.
				_ = srv.Close()
				record(err)
			}
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, cancel := context.WithTimeout(ctx, proxyShutdownBudget)
			defer cancel()
			// Drain normal/SSE requests and hijacked WebSocket tunnels
			// within the SAME budget (§22.3): http.Server.Shutdown ignores
			// hijacked conns, so tunnels are drained explicitly.
			var inner sync.WaitGroup
			inner.Add(2)
			go func() {
				defer inner.Done()
				if err := a.proxySrv.Shutdown(c); err != nil {
					_ = a.proxySrv.Close()
					record(err)
				}
			}()
			go func() {
				defer inner.Done()
				a.proxyHandler.DrainWebSockets(c)
			}()
			inner.Wait()
		}()
		if a.adminSrv != nil {
			wg.Add(1)
			go stop(a.adminSrv, adminShutdownBudget)
		}
		wg.Wait()

		if a.watcher != nil {
			record(a.watcher.Close())
		}
		a.wg.Wait()
		a.shutErr = firstErr
		a.logger.Info("shutdown complete")
	})
	return a.shutErr
}

// Close force-closes both servers immediately (second-signal path). Active
// connections are dropped.
func (a *App) Close() {
	_ = a.proxySrv.Close()
	if a.adminSrv != nil {
		_ = a.adminSrv.Close()
	}
	if a.watcher != nil {
		_ = a.watcher.Close()
	}
}

// isLoopbackListen reports whether a listen address is bound to a loopback
// interface. An empty host (":9090") binds all interfaces → not loopback.
func isLoopbackListen(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil || host == "" {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
