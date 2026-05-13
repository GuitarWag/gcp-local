package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/GuitarWag/gcp-local/internal/auth"
	"github.com/GuitarWag/gcp-local/internal/config"
	"github.com/GuitarWag/gcp-local/internal/gateway"
	"github.com/GuitarWag/gcp-local/internal/pidfile"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	switch cmd {
	case "start":
		if err := runStart(args); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	case "env":
		runEnv(args)
	case "status":
		os.Exit(runStatus())
	case "stop":
		os.Exit(runStop())
	case "reset":
		if err := runReset(args); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintln(os.Stderr, "unknown command:", cmd)
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `gcp-local — local GCP emulator

Usage:
  gcp-local start [--port=N] [--config=FILE] [--no-daemon]
  gcp-local env
  gcp-local status
  gcp-local stop
  gcp-local reset [--service=NAME]
`)
}

func runStart(args []string) error {
	fs := flag.NewFlagSet("start", flag.ExitOnError)
	port := fs.Int("port", 4443, "port to listen on")
	cfgPath := fs.String("config", "", "config file path")
	noDaemon := fs.Bool("no-daemon", false, "run in foreground")
	daemonChild := fs.Bool("__daemon-child", false, "internal: running as daemon child")
	_ = daemonChild
	if err := fs.Parse(args); err != nil {
		return err
	}

	if !*noDaemon && !*daemonChild {
		return startDaemon(args, *port)
	}

	cfg, err := loadConfig(*cfgPath, *port)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	credsPath, err := auth.WriteFakeCreds(cfg.Project)
	if err != nil {
		return fmt.Errorf("write creds: %w", err)
	}

	gw, err := gateway.New(cfg)
	if err != nil {
		return fmt.Errorf("init gateway: %w", err)
	}

	if err := pidfile.Write(pidfile.Info{
		PID:  os.Getpid(),
		Port: cfg.Port,
		Host: fmt.Sprintf("localhost:%d", cfg.Port),
	}); err != nil {
		return fmt.Errorf("pidfile: %w", err)
	}
	defer func() { _ = pidfile.Remove() }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	fmt.Fprintf(os.Stderr, "gcp-local listening on :%d (project=%s)\n", cfg.Port, cfg.Project)
	fmt.Fprintf(os.Stderr, "credentials: %s\n", credsPath)
	return gw.Run(ctx)
}

func startDaemon(args []string, port int) error {
	// Spawn ourselves with --__daemon-child marker, detached. Wait for /healthz.
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	childArgs := append([]string{"start", "--__daemon-child"}, args...)
	cmd := exec.Command(exe, childArgs...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("spawn daemon: %w", err)
	}
	pid := cmd.Process.Pid
	if err := cmd.Process.Release(); err != nil {
		return fmt.Errorf("release: %w", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(fmt.Sprintf("http://localhost:%d/healthz", port))
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				fmt.Printf("gcp-local started on :%d (pid=%d)\n", port, pid)
				return nil
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("daemon failed to become ready within 5s")
}

func runEnv(args []string) {
	fs := flag.NewFlagSet("env", flag.ExitOnError)
	port := fs.Int("port", 0, "port the emulator is on (defaults to pidfile)")
	project := fs.String("project", "local-project", "project id")
	_ = fs.Parse(args)

	resolved := *port
	if resolved == 0 {
		if info, err := pidfile.Read(); err == nil {
			resolved = info.Port
		}
	}
	if resolved == 0 {
		resolved = 4443
	}

	host := fmt.Sprintf("localhost:%d", resolved)
	credsPath := defaultCredsPath()

	exports := []string{
		fmt.Sprintf("export STORAGE_EMULATOR_HOST=http://%s", host),
		fmt.Sprintf("export PUBSUB_EMULATOR_HOST=%s", host),
		fmt.Sprintf("export FIRESTORE_EMULATOR_HOST=%s", host),
		fmt.Sprintf("export BIGTABLE_EMULATOR_HOST=%s", host),
		fmt.Sprintf("export SPANNER_EMULATOR_HOST=%s", host),
		fmt.Sprintf("export GOOGLE_CLOUD_PROJECT=%s", *project),
		fmt.Sprintf("export GOOGLE_APPLICATION_CREDENTIALS=%s", credsPath),
	}
	for _, e := range exports {
		fmt.Println(e)
	}
}

func runStatus() int {
	info, err := pidfile.Read()
	if err != nil {
		fmt.Println("status: not running (no pidfile)")
		return 1
	}
	if !pidfile.Alive(info.PID) {
		fmt.Printf("status: stale pidfile (pid=%d not alive)\n", info.PID)
		return 1
	}
	resp, err := http.Get(fmt.Sprintf("http://%s/healthz", info.Host))
	if err != nil {
		fmt.Printf("status: pid=%d alive, healthz unreachable: %v\n", info.PID, err)
		return 2
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		fmt.Printf("status: pid=%d alive, /healthz=%d\n", info.PID, resp.StatusCode)
		return 2
	}
	var parsed map[string]any
	_ = json.Unmarshal(body, &parsed)
	fmt.Printf("status: ok %s\n", pidfile.String(*info))
	if svcs, ok := parsed["services"].(map[string]any); ok {
		for k, v := range svcs {
			fmt.Printf("  %s: %v\n", k, v)
		}
	}
	return 0
}

func runStop() int {
	info, err := pidfile.Read()
	if err != nil {
		fmt.Println("stop: not running (no pidfile)")
		return 1
	}
	if !pidfile.Alive(info.PID) {
		_ = pidfile.Remove()
		fmt.Println("stop: stale pidfile removed")
		return 0
	}
	if err := pidfile.SignalTerm(info.PID); err != nil {
		fmt.Printf("stop: signal failed: %v\n", err)
		return 1
	}
	for i := 0; i < 50; i++ {
		if !pidfile.Alive(info.PID) {
			_ = pidfile.Remove()
			fmt.Printf("stop: pid=%d terminated\n", info.PID)
			return 0
		}
		time.Sleep(100 * time.Millisecond)
	}
	fmt.Printf("stop: pid=%d still alive after 5s\n", info.PID)
	return 1
}

func runReset(args []string) error {
	fs := flag.NewFlagSet("reset", flag.ExitOnError)
	_ = fs.String("service", "", "limit reset to one service (currently ignored)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	info, err := pidfile.Read()
	if err != nil {
		return fmt.Errorf("not running (no pidfile)")
	}
	url := fmt.Sprintf("http://%s/admin/reset", info.Host)
	req, _ := http.NewRequest(http.MethodPost, url, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("reset failed: %d %s", resp.StatusCode, body)
	}
	fmt.Println("reset: ok")
	return nil
}

func loadConfig(path string, portOverride int) (*config.Config, error) {
	cfg, err := config.Load(path)
	if err != nil {
		return nil, err
	}
	if portOverride != 0 {
		cfg.Port = portOverride
	}
	return cfg, nil
}

func defaultCredsPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "/tmp/gcp-local-creds.json"
	}
	return filepath.Join(home, ".gcp-local", "fake-creds.json")
}
