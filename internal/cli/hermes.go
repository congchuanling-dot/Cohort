package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"cohort/internal/app"
	"cohort/internal/evaluation"
	"cohort/internal/hermes"
)

func runHermesCommand(ctx context.Context, cfg app.Config, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: cohort hermes start|stop|status|logs|serve|actions|jobs|repairs")
	}
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	store := hermes.NewStore(root)
	if err := store.Ensure(); err != nil {
		return err
	}
	switch args[0] {
	case "start":
		if len(args) == 2 && args[1] == "--foreground" {
			return hermesServe(ctx, root, cfg, out)
		}
		return hermesStart(store, args[1:], out)
	case "stop":
		return hermesStop(store, out)
	case "status":
		return hermesStatus(store, out)
	case "logs":
		return hermesLogs(store, out)
	case "serve":
		return hermesServe(ctx, root, cfg, out)
	case "actions":
		return hermesActions(ctx, root, cfg, store, args[1:], out)
	case "jobs":
		return hermesJobs(ctx, root, cfg, store, args[1:], out)
	case "repairs":
		return hermesRepairs(ctx, root, cfg, store, args[1:], out)
	default:
		return fmt.Errorf("unknown hermes command %q", args[0])
	}
}

func hermesStart(store hermes.Store, args []string, out io.Writer) error {
	for _, arg := range args {
		return fmt.Errorf("unknown hermes start option %q", arg)
	}
	status, _ := store.LoadStatus()
	if pidFromFile, ok := readPID(store.PIDPath()); ok {
		status.PID = pidFromFile
	}
	if isPIDRunning(status.PID) {
		fmt.Fprintf(out, "already_running: true\npid: %d\n", status.PID)
		return nil
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	logFile, err := os.OpenFile(store.LogPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	defer logFile.Close()
	cmd := exec.Command(executable, "hermes", "serve")
	cmd.Dir = filepath.Dir(filepath.Dir(store.Root))
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		return err
	}
	status.Running = true
	status.PID = cmd.Process.Pid
	status.StartedAt = time.Now().UTC()
	cfg, _ := store.LoadConfig()
	status.Config = cfg
	if err := store.SaveStatus(status); err != nil {
		return err
	}
	if err := os.WriteFile(store.PIDPath(), []byte(fmt.Sprintf("%d\n", cmd.Process.Pid)), 0644); err != nil {
		return err
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !isPIDRunning(cmd.Process.Pid) {
			return fmt.Errorf("hermes process %d exited during startup; inspect %s", cmd.Process.Pid, store.LogPath())
		}
		current, loadErr := store.LoadStatus()
		if loadErr == nil && current.Running && current.PID == cmd.Process.Pid {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	fmt.Fprintf(out, "started: true\npid: %d\nlog: %s\n", cmd.Process.Pid, store.LogPath())
	return cmd.Process.Release()
}

func hermesStop(store hermes.Store, out io.Writer) error {
	status, _ := store.LoadStatus()
	if pidFromFile, ok := readPID(store.PIDPath()); ok {
		status.PID = pidFromFile
	}
	if !isPIDRunning(status.PID) {
		status.Running = false
		status.PID = 0
		_ = os.Remove(store.PIDPath())
		_ = store.SaveStatus(status)
		fmt.Fprintln(out, "running: false")
		return nil
	}
	process, err := os.FindProcess(status.PID)
	if err != nil {
		return err
	}
	if err := process.Signal(syscall.SIGTERM); err != nil {
		return err
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !isPIDRunning(status.PID) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if isPIDRunning(status.PID) {
		return fmt.Errorf("hermes pid %d did not stop within timeout", status.PID)
	}
	status.Running = false
	status.PID = 0
	_ = os.Remove(store.PIDPath())
	if err := store.SaveStatus(status); err != nil {
		return err
	}
	fmt.Fprintln(out, "stopped: true")
	return nil
}

func hermesStatus(store hermes.Store, out io.Writer) error {
	status, err := store.LoadStatus()
	if err != nil {
		return err
	}
	if pidFromFile, ok := readPID(store.PIDPath()); ok {
		status.PID = pidFromFile
	}
	status.Running = isPIDRunning(status.PID)
	queue, _ := store.LoadQueue()
	open, critical, high := hermes.CountOpen(queue)
	status.OpenActions = open
	status.CriticalActions = critical
	status.HighActions = high
	alerts, _ := store.LoadAlerts(5)
	status.LastAlerts = alerts
	if err := store.SaveStatus(status); err != nil {
		return err
	}
	data, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		return err
	}
	fmt.Fprintln(out, string(data))
	return nil
}

func hermesLogs(store hermes.Store, out io.Writer) error {
	data, err := os.ReadFile(store.LogPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			fmt.Fprintln(out, "no logs")
			return nil
		}
		return err
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > 80 {
		lines = lines[len(lines)-80:]
	}
	for _, line := range lines {
		fmt.Fprintln(out, line)
	}
	return nil
}

func hermesServe(ctx context.Context, root string, cfg app.Config, out io.Writer) error {
	service, err := hermes.NewService(root)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	configureHermesEvalRunner(service, cfg, out)
	configureHermesRepairWorker(service, cfg, out)
	fmt.Fprintf(out, "hermes serve pid=%d\n", os.Getpid())
	return service.Serve(ctx)
}

func hermesActions(ctx context.Context, root string, cfg app.Config, store hermes.Store, args []string, out io.Writer) error {
	if len(args) == 0 || args[0] == "list" {
		service, err := hermes.NewService(root)
		if err == nil {
			_, _, _, _ = service.RefreshStability()
		}
		return hermesActionsList(store, out)
	}
	switch args[0] {
	case "show":
		if len(args) != 2 {
			return errors.New("usage: cohort hermes actions show <id>")
		}
		return hermesActionsShow(store, args[1], out)
	case "ack":
		if len(args) != 2 {
			return errors.New("usage: cohort hermes actions ack <id>")
		}
		action, err := hermes.UpdateActionStatus(store, args[1], hermes.QueueStatusAcknowledged)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "acknowledged: %s\n", action.ID)
		return nil
	case "start":
		if len(args) != 2 {
			return errors.New("usage: cohort hermes actions start <id>")
		}
		action, err := hermes.UpdateActionStatus(store, args[1], hermes.QueueStatusInProgress)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "in_progress: %s\n", action.ID)
		return nil
	case "resolve":
		if len(args) != 4 || args[2] != "--with-run" {
			return errors.New("usage: cohort hermes actions resolve <id> --with-run <run_id>")
		}
		action, err := hermes.VerifyActionWithRun(store, evaluation.NewStore(root), args[1], args[3], true)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "resolved: %s\nverification_run: %s\n", action.ID, action.VerificationRunID)
		return nil
	case "verify":
		if len(args) != 2 {
			return errors.New("usage: cohort hermes actions verify <id>")
		}
		action, err := runHermesActionVerification(ctx, root, cfg, store, args[1], out)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "verified: %s\nverification_run: %s\n", action.ID, action.VerificationRunID)
		return nil
	case "repair":
		if len(args) < 2 || len(args) > 3 || len(args) == 3 && args[2] != "--run" {
			return errors.New("usage: cohort hermes actions repair <id> [--run]")
		}
		repairArgs := []string{"create", args[1]}
		if len(args) == 3 {
			repairArgs = append(repairArgs, "--run")
		}
		return hermesRepairs(ctx, root, cfg, store, repairArgs, out)
	case "dismiss":
		if len(args) != 2 {
			return errors.New("usage: cohort hermes actions dismiss <id>")
		}
		action, err := hermes.UpdateActionStatus(store, args[1], hermes.QueueStatusDismissed)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "dismissed: %s\n", action.ID)
		return nil
	default:
		return fmt.Errorf("unknown hermes actions command %q", args[0])
	}
}

func hermesActionsList(store hermes.Store, out io.Writer) error {
	queue, err := store.LoadQueue()
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tSTATUS\tSEVERITY\tCATEGORY\tCASE\tTITLE")
	for _, action := range queue.Actions {
		if action.Status == hermes.QueueStatusResolved || action.Status == hermes.QueueStatusDismissed {
			continue
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s/%s\t%s\n", action.ID, action.Status, action.Severity, action.Category, action.SuiteID, action.CaseID, action.Title)
	}
	return w.Flush()
}

func hermesActionsShow(store hermes.Store, id string, out io.Writer) error {
	queue, err := store.LoadQueue()
	if err != nil {
		return err
	}
	for _, action := range queue.Actions {
		if action.ID == id || action.Fingerprint == id {
			data, err := json.MarshalIndent(action, "", "  ")
			if err != nil {
				return err
			}
			fmt.Fprintln(out, string(data))
			return nil
		}
	}
	return fmt.Errorf("action %q not found", id)
}

func readPID(path string) (int, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	return pid, err == nil && pid > 0
}

func isPIDRunning(pid int) bool {
	if pid <= 0 {
		return false
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return process.Signal(syscall.Signal(0)) == nil
}
