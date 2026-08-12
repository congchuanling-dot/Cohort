package controlactions

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"cohort/internal/controlplane"
	"cohort/internal/evaluation"
	"cohort/internal/session"
	"cohort/internal/traceview"
	"cohort/internal/tuning"
)

func NewQualityProvider() controlplane.QualityProvider {
	return func(ctx context.Context, projectRoot string, segments []string, query url.Values) (any, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		store := evaluation.NewStore(projectRoot)
		switch {
		case len(segments) == 1 && segments[0] == "summary":
			results, err := store.ListResults()
			if err != nil {
				return nil, err
			}
			index := evaluation.BuildStabilityIndex(results, evaluation.StabilityOptions{Window: 20})
			if len(index.Runs) > 20 {
				index.Runs = index.Runs[len(index.Runs)-20:]
			}
			return map[string]any{
				"summary": index.Summary,
				"runs":    index.Runs,
				"suites":  index.Suites,
			}, nil
		case len(segments) == 2 && segments[0] == "evals":
			result, err := store.LoadResult(segments[1])
			if err != nil {
				return nil, err
			}
			return evaluation.BuildDashboardData(store, result)
		case len(segments) == 1 && segments[0] == "stability":
			results, err := store.ListResults()
			if err != nil {
				return nil, err
			}
			return evaluation.BuildStabilityIndex(results, stabilityOptions(query)), nil
		case len(segments) == 3 && segments[0] == "traces":
			view, err := loadQualityTrace(projectRoot, segments[1], segments[2])
			if err != nil {
				return nil, err
			}
			return map[string]any{
				"graph": view.CausalGraph(), "summary": view.Summary(), "receipts": view.ReceiptLedger(),
			}, nil
		case len(segments) == 3 && segments[0] == "receipts":
			view, err := loadQualityTrace(projectRoot, segments[1], segments[2])
			if err != nil {
				return nil, err
			}
			return view.ReceiptLedger(), nil
		case len(segments) == 1 && segments[0] == "tuning":
			limit := boundedLimit(query.Get("limit"), 50, 500)
			return tuning.Analyze(projectRoot, tuning.Options{
				SessionRoot: filepath.Join(projectRoot, session.DefaultRootDir), Limit: limit,
			})
		default:
			return nil, os.ErrNotExist
		}
	}
}

func NewExportProvider() controlplane.ExportProvider {
	return func(ctx context.Context, projectRoot string, segments []string, query url.Values) (controlplane.ExportResult, error) {
		if err := ctx.Err(); err != nil {
			return controlplane.ExportResult{}, err
		}
		store := evaluation.NewStore(projectRoot)
		switch {
		case len(segments) == 2 && segments[0] == "evals":
			runID, ok := trimHTMLSuffix(segments[1])
			if !ok {
				return controlplane.ExportResult{}, os.ErrNotExist
			}
			result, err := store.LoadResult(runID)
			if err != nil {
				return controlplane.ExportResult{}, err
			}
			data, err := evaluation.DashboardHTML(store, result)
			if err != nil {
				return controlplane.ExportResult{}, err
			}
			return htmlExport("cohort-eval-"+safeExportName(runID)+".html", data), nil
		case len(segments) == 1 && segments[0] == "stability.html":
			results, err := store.ListResults()
			if err != nil {
				return controlplane.ExportResult{}, err
			}
			data, err := evaluation.StabilityHTML(evaluation.BuildStabilityIndex(results, stabilityOptions(query)))
			if err != nil {
				return controlplane.ExportResult{}, err
			}
			return htmlExport("cohort-eval-stability.html", data), nil
		case len(segments) == 3 && segments[0] == "traces":
			runID, ok := trimHTMLSuffix(segments[2])
			if !ok {
				return controlplane.ExportResult{}, os.ErrNotExist
			}
			view, err := loadQualityTrace(projectRoot, segments[1], runID)
			if err != nil {
				return controlplane.ExportResult{}, err
			}
			data, err := traceview.GraphHTML(view)
			if err != nil {
				return controlplane.ExportResult{}, err
			}
			return htmlExport("cohort-trace-"+safeExportName(runID)+".html", data), nil
		case len(segments) == 1 && segments[0] == "tuning.html":
			report, err := tuning.Analyze(projectRoot, tuning.Options{
				SessionRoot: filepath.Join(projectRoot, session.DefaultRootDir),
				Limit:       boundedLimit(query.Get("limit"), 50, 500),
			})
			if err != nil {
				return controlplane.ExportResult{}, err
			}
			data, err := tuning.DashboardHTML(report)
			if err != nil {
				return controlplane.ExportResult{}, err
			}
			return htmlExport("cohort-runtime-tuning.html", data), nil
		default:
			return controlplane.ExportResult{}, os.ErrNotExist
		}
	}
}

func loadQualityTrace(projectRoot, sessionID, runID string) (traceview.RunView, error) {
	roots := []string{
		filepath.Join(projectRoot, session.DefaultRootDir),
		evaluation.NewStore(projectRoot).SessionsDir(),
	}
	var lastErr error
	for _, root := range roots {
		view, err := traceview.LoadSessionRun(root, sessionID, runID)
		if err == nil {
			return view, nil
		}
		lastErr = err
	}
	return traceview.RunView{}, lastErr
}

func stabilityOptions(query url.Values) evaluation.StabilityOptions {
	window := 20
	if value, err := strconv.Atoi(query.Get("window")); err == nil && value >= 0 && value <= 1000 {
		window = value
	}
	return evaluation.StabilityOptions{
		Window: window, SuiteID: strings.TrimSpace(query.Get("suite")),
		Profile: strings.TrimSpace(query.Get("profile")), Model: strings.TrimSpace(query.Get("model")),
		OnlyFlaky: query.Get("flaky") == "true",
	}
}

func boundedLimit(value string, fallback, maximum int) int {
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return min(parsed, maximum)
}

func trimHTMLSuffix(value string) (string, bool) {
	if !strings.HasSuffix(value, ".html") {
		return "", false
	}
	value = strings.TrimSuffix(value, ".html")
	return value, value != ""
}

func safeExportName(value string) string {
	return strings.Map(func(char rune) rune {
		switch {
		case char >= 'a' && char <= 'z', char >= 'A' && char <= 'Z', char >= '0' && char <= '9', char == '-', char == '_':
			return char
		default:
			return '-'
		}
	}, value)
}

func htmlExport(filename string, data []byte) controlplane.ExportResult {
	return controlplane.ExportResult{
		Filename: filename, ContentType: "text/html; charset=utf-8", Data: data,
	}
}
