package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"cohort/internal/consoleui"
	"cohort/internal/controlactions"
	"cohort/internal/controlplane"
	"cohort/internal/delivery"
)

type uiOptions struct {
	Listen string
	Open   bool
}

func runUICommand(ctx context.Context, args []string, out io.Writer) error {
	options, err := parseUIOptions(args)
	if err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	projectRoot, err := delivery.RepositoryRoot(ctx, cwd)
	if err != nil {
		return err
	}
	catalog, err := controlactions.NewCatalog()
	if err != nil {
		return err
	}
	assets, err := consoleui.Assets()
	if err != nil {
		return err
	}
	server, err := controlplane.NewServer(controlplane.ServerConfig{
		ProjectRoot: projectRoot,
		Listen:      options.Listen,
		StaticFS:    assets,
		Catalog:     catalog,
	})
	if err != nil {
		return err
	}
	serverCtx, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer cancel()
	running, err := server.Start(serverCtx)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "control_center: %s\n", running.BootstrapURL)
	fmt.Fprintf(out, "listen: %s\n", running.Address)
	fmt.Fprintf(out, "project_root: %s\n", projectRoot)
	if options.Open {
		if err := openControlCenter(running.BootstrapURL); err != nil {
			_ = running.Close(context.Background())
			return err
		}
		fmt.Fprintln(out, "opened: true")
	}
	<-serverCtx.Done()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	return running.Close(shutdownCtx)
}

func parseUIOptions(args []string) (uiOptions, error) {
	options := uiOptions{Listen: "127.0.0.1:0", Open: true}
	for index := 0; index < len(args); index++ {
		switch {
		case args[index] == "--no-open":
			options.Open = false
		case args[index] == "--listen":
			if index+1 >= len(args) {
				return options, errors.New("--listen requires host:port")
			}
			options.Listen = strings.TrimSpace(args[index+1])
			index++
		case strings.HasPrefix(args[index], "--listen="):
			options.Listen = strings.TrimSpace(strings.TrimPrefix(args[index], "--listen="))
		default:
			return options, fmt.Errorf("unknown ui option %q", args[index])
		}
	}
	return options, nil
}

func openControlCenter(target string) error {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.Command("open", target)
	case "windows":
		command = exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
	default:
		command = exec.Command("xdg-open", target)
	}
	if err := command.Start(); err != nil {
		return fmt.Errorf("open control center: %w", err)
	}
	return command.Process.Release()
}
