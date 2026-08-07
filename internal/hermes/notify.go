package hermes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func DeliverAlerts(ctx context.Context, store Store, configs []NotificationConfig, alerts []Alert, stdout io.Writer) error {
	var failures []string
	for _, cfg := range configs {
		if !cfg.Enabled {
			continue
		}
		for _, alert := range alerts {
			if severityRank(alert.Severity) < severityRank(firstNonEmptyString(cfg.MinSeverity, AlertSeverityHigh)) {
				continue
			}
			if err := deliverAlert(ctx, store, cfg, alert, stdout); err != nil {
				failures = append(failures, fmt.Sprintf("%s: %v", cfg.ID, err))
			}
		}
	}
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "; "))
	}
	return nil
}

func deliverAlert(ctx context.Context, store Store, cfg NotificationConfig, alert Alert, stdout io.Writer) error {
	event := Event{
		ID:       alert.ID,
		Time:     alert.Time,
		Type:     "eval_action_alert",
		Severity: alert.Severity,
		SourceID: alert.ActionID,
		Data:     alert,
	}
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	switch strings.ToLower(strings.TrimSpace(cfg.Type)) {
	case "stdout":
		if stdout == nil {
			stdout = io.Discard
		}
		_, err = fmt.Fprintf(stdout, "[hermes-notification] %s\n", data)
		return err
	case "file":
		target := strings.TrimSpace(cfg.Target)
		if target == "" {
			target = filepath.Join(store.Root, "notifications.jsonl")
		} else if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(filepath.Dir(store.Root)), target)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		file, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			return err
		}
		defer file.Close()
		_, err = file.Write(append(data, '\n'))
		return err
	case "webhook":
		if strings.TrimSpace(cfg.Target) == "" {
			return errors.New("webhook target is required")
		}
		timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
		if timeout <= 0 {
			timeout = 5 * time.Second
		}
		requestCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, cfg.Target, bytes.NewReader(data))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		response, err := (&http.Client{Timeout: timeout}).Do(req)
		if err != nil {
			return err
		}
		defer response.Body.Close()
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			_, _ = io.Copy(io.Discard, response.Body)
			return fmt.Errorf("webhook returned %s", response.Status)
		}
		return nil
	default:
		return fmt.Errorf("unsupported notification type %q", cfg.Type)
	}
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
