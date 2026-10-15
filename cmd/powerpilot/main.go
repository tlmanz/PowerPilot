// Command powerpilot is the PowerPilot UPS monitor. "app" (the default) shows
// the dashboard, installing the background service first if it isn't running;
// "serve" runs that service; "install" and "uninstall" manage it.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/tlmanz/PowerPilot/internal/app"
	"github.com/tlmanz/PowerPilot/internal/monitor"
	"github.com/tlmanz/PowerPilot/internal/service"
	"github.com/tlmanz/PowerPilot/internal/settings"
	"github.com/tlmanz/PowerPilot/internal/system"
	"github.com/tlmanz/PowerPilot/internal/update"
	"github.com/tlmanz/PowerPilot/internal/ups"
	"github.com/tlmanz/PowerPilot/internal/web"
)

const defaultAddr = "127.0.0.1:8484"

// Set at build time: -ldflags "-X main.version=v1.2.3".
var version = "dev"

const usage = `usage: powerpilot [app]          open the dashboard (starts the service if needed)
       powerpilot serve [flags]  run the service in the foreground
       powerpilot install        start the service at login
       powerpilot uninstall      stop the service and remove it from login
       powerpilot version`

func main() {
	args := os.Args[1:]
	cmd := "app"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	switch cmd {
	case "app":
		openWindow(args)
	case "serve":
		serve(args)
	case "install":
		report("PowerPilot will now run in the background and start at login.\nDashboard: http://"+defaultAddr, installService())
	case "uninstall":
		report("PowerPilot's background service was removed. Your settings are kept.", service.Uninstall())
	case "version":
		fmt.Println(version)
	default:
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
}

func serve(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("listen", defaultAddr, "dashboard address; keep it on localhost")
	interval := fs.Duration("interval", 2*time.Second, "how often to poll the UPS")
	brand := fs.String("brand", "Prolink", "UPS brand shown on the dashboard")
	model := fs.String("model", "PRO2000SFCU", "UPS model shown on the dashboard")
	ratedVA := fs.Int("rated-va", 2000, "UPS apparent power rating in VA, from its label")
	ratedW := fs.Int("rated-w", 1200, "UPS real power rating in W, from its label")
	logPath := fs.String("log", defaultLog(), "log file (empty: standard error)")
	fs.Parse(args)

	log := slog.New(slog.NewTextHandler(logWriter(*logPath), nil))

	dir, err := service.ConfigDir()
	if err != nil {
		log.Error("no config directory", "err", err)
		os.Exit(1)
	}
	migrateSettings(dir)
	store, err := settings.Open(filepath.Join(dir, "settings.json"))
	if err != nil {
		log.Warn("using default settings", "err", err)
	}
	if err := os.WriteFile(service.PIDFile(), []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
		log.Warn("could not write PID file", "err", err)
	}
	defer os.Remove(service.PIDFile())

	device := monitor.Device{
		Brand:      *brand,
		Model:      *model,
		Protocol:   "Megatec (ViewPower PMV)",
		Connection: fmt.Sprintf("USB %04X:%04X", ups.VendorID, ups.ProductID),
		RatedVA:    *ratedVA,
		RatedW:     *ratedW,
		AppVersion: version,
	}
	mon := monitor.New(*interval, device, store, monitor.Actions{Notify: system.Notify, ShutDown: system.ShutDown}, log)

	exe, err := executable()
	if err != nil {
		log.Error("cannot find the running program", "err", err)
		os.Exit(1)
	}
	if from := os.Getenv(update.UpdatedFromEnv); from != "" {
		os.Unsetenv(update.UpdatedFromEnv)
		mon.Note("good", fmt.Sprintf("Updated PowerPilot from %s to %s", from, version))
		if err := service.Refresh(exe, web.Logo()); err != nil {
			log.Warn("could not refresh the app-menu entry", "err", err)
		}
	}
	go update.Cleanup(exe)

	handler, err := web.Handler(mon, *addr, service.WindowFile(), update.New(version, exe))
	if err != nil {
		log.Error("bad listen address", "err", err)
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	srv := &http.Server{
		Addr:              *addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		// Cancels open event streams on shutdown.
		BaseContext: func(net.Listener) context.Context { return ctx },
	}

	monDone := make(chan struct{})
	go func() {
		mon.Run(ctx.Done())
		close(monDone)
	}()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)
	}()

	ln, err := listen(*addr)
	if err != nil {
		log.Error("cannot listen", "err", err)
		os.Exit(1)
	}
	log.Info("dashboard listening", "url", "http://"+*addr, "version", version)
	err = srv.Serve(ln)
	stop()
	<-monDone
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("server failed", "err", err)
		os.Exit(1)
	}
}

func openWindow(args []string) {
	fs := flag.NewFlagSet("app", flag.ExitOnError)
	url := fs.String("url", "http://"+defaultAddr, "dashboard URL")
	noInstall := fs.Bool("no-install", false, "don't install the service when it isn't running")
	// Older macOS versions pass -psn_* when launching from Finder.
	var kept []string
	for _, a := range args {
		if !strings.HasPrefix(a, "-psn_") {
			kept = append(kept, a)
		}
	}
	fs.Parse(kept)

	if !*noInstall && !reachable(*url) {
		if err := installService(); err != nil {
			system.Alert("PowerPilot", "Could not start the PowerPilot service: "+err.Error())
		} else {
			for deadline := time.Now().Add(15 * time.Second); !reachable(*url) && time.Now().Before(deadline); {
				time.Sleep(300 * time.Millisecond)
			}
		}
	}
	app.Run(*url)
}

func installService() error {
	exe, err := executable()
	if err != nil {
		return err
	}
	return service.Install(exe, web.Logo())
}

// executable is the running program's real path, symlinks resolved.
func executable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

// listen retries for a while: after an update on Windows the previous process
// may still hold the port for a moment.
func listen(addr string) (net.Listener, error) {
	var err error
	for range 50 {
		var ln net.Listener
		if ln, err = net.Listen("tcp", addr); err == nil {
			return ln, nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return nil, err
}

func reachable(url string) bool {
	c := http.Client{Timeout: time.Second}
	resp, err := c.Get(url + "/api/state")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// report prints the outcome, or shows a dialog on Windows, whose GUI build
// has no console.
func report(ok string, err error) {
	msg := ok
	if err != nil {
		msg = "Error: " + err.Error()
	}
	if runtime.GOOS == "windows" {
		system.Alert("PowerPilot", msg)
	} else {
		fmt.Println(msg)
	}
	if err != nil {
		os.Exit(1)
	}
}

// defaultLog is a file on Windows (no console there) and standard error
// elsewhere, where launchd and systemd capture it.
func defaultLog() string {
	if runtime.GOOS != "windows" {
		return ""
	}
	dir, err := service.ConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "powerpilot.log")
}

func logWriter(path string) io.Writer {
	if path == "" {
		return os.Stderr
	}
	os.MkdirAll(filepath.Dir(path), 0o755)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return os.Stderr
	}
	return f
}

// migrateSettings moves the config folder from builds named "UPS Monitor".
func migrateSettings(dir string) {
	if _, err := os.Stat(dir); err == nil {
		return
	}
	old := filepath.Join(filepath.Dir(dir), "UPS Monitor")
	if _, err := os.Stat(old); err == nil {
		os.Rename(old, dir)
		return
	}
	os.MkdirAll(dir, 0o700)
}
