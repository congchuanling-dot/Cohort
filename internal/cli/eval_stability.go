package cli

import (
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"

	"cohort/internal/evaluation"
)

type evalStabilityCLIOptions struct {
	evaluation.StabilityOptions
	Open bool
}

func runEvalStabilityCommand(store evaluation.Store, args []string, out io.Writer) error {
	if len(args) == 0 {
		args = []string{"report"}
	} else if strings.HasPrefix(args[0], "-") {
		args = append([]string{"report"}, args...)
	}
	command := args[0]
	opts, err := parseEvalStabilityOptions(args[1:])
	if err != nil {
		return err
	}
	results, err := store.ListResults()
	if err != nil {
		return err
	}
	if len(results) == 0 {
		return errors.New("no eval runs; run cohort eval run first")
	}
	index := evaluation.BuildStabilityIndex(results, opts.StabilityOptions)
	indexPath, markdownPath, htmlPath, err := evaluation.WriteStabilityReports(store, index)
	if err != nil {
		return err
	}
	switch command {
	case "update":
		fmt.Fprintf(out, "index: %s\nmarkdown: %s\ndashboard: %s\n", indexPath, markdownPath, htmlPath)
	case "report":
		fmt.Fprintf(out, "runs: %d\nflaky_cases: %d\nregressions: %d\nindex: %s\nmarkdown: %s\ndashboard: %s\n",
			index.Summary.Runs, index.Summary.FlakyCases, index.Summary.Regressions, indexPath, markdownPath, htmlPath)
		if opts.Open {
			if err := exec.Command("open", htmlPath).Start(); err != nil {
				return fmt.Errorf("open stability dashboard: %w", err)
			}
			fmt.Fprintln(out, "opened: true")
		}
	case "cases":
		printStabilityCases(index, out)
	case "regressions":
		printStabilityRegressions(index, out)
	default:
		return fmt.Errorf("unknown eval stability command %q", command)
	}
	return nil
}

func refreshEvalStability(store evaluation.Store, out io.Writer) error {
	results, err := store.ListResults()
	if err != nil {
		return err
	}
	if len(results) == 0 {
		return nil
	}
	index := evaluation.BuildStabilityIndex(results, evaluation.StabilityOptions{Window: 20})
	_, _, htmlPath, err := evaluation.WriteStabilityReports(store, index)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "stability: refreshed %s\n", htmlPath)
	return nil
}

func parseEvalStabilityOptions(args []string) (evalStabilityCLIOptions, error) {
	opts := evalStabilityCLIOptions{StabilityOptions: evaluation.StabilityOptions{Window: 20}}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--window" || arg == "--suite" || arg == "--profile" || arg == "--model":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("%s requires a value", arg)
			}
			value := args[i+1]
			i++
			switch arg {
			case "--window":
				window, err := strconv.Atoi(value)
				if err != nil || window < 0 || window > 1000 {
					return opts, errors.New("--window must be between 0 and 1000")
				}
				opts.Window = window
			case "--suite":
				opts.SuiteID = strings.TrimSpace(value)
			case "--profile":
				opts.Profile = strings.TrimSpace(value)
			case "--model":
				opts.Model = strings.TrimSpace(value)
			}
		case strings.HasPrefix(arg, "--window="):
			window, err := strconv.Atoi(strings.TrimPrefix(arg, "--window="))
			if err != nil || window < 0 || window > 1000 {
				return opts, errors.New("--window must be between 0 and 1000")
			}
			opts.Window = window
		case strings.HasPrefix(arg, "--suite="):
			opts.SuiteID = strings.TrimSpace(strings.TrimPrefix(arg, "--suite="))
		case strings.HasPrefix(arg, "--profile="):
			opts.Profile = strings.TrimSpace(strings.TrimPrefix(arg, "--profile="))
		case strings.HasPrefix(arg, "--model="):
			opts.Model = strings.TrimSpace(strings.TrimPrefix(arg, "--model="))
		case arg == "--open":
			opts.Open = true
		case arg == "--flaky":
			opts.OnlyFlaky = true
		default:
			return opts, fmt.Errorf("unknown eval stability option %q", arg)
		}
	}
	return opts, nil
}

func printStabilityCases(index evaluation.StabilityIndex, out io.Writer) {
	fmt.Fprintln(out, "SUITE\tCASE\tPASS RATE\tSTABILITY\tFLAKY\tREGRESSIONS\tLATEST")
	for _, c := range index.Cases {
		fmt.Fprintf(out, "%s\t%s\t%.1f%%\t%.1f%%\t%t\t%d\t%s\n",
			c.SuiteID, c.CaseID, c.PassRate, c.AverageStability, c.Flaky, c.Regressions, c.LatestRunID)
	}
}

func printStabilityRegressions(index evaluation.StabilityIndex, out io.Writer) {
	if len(index.Regressions) == 0 {
		fmt.Fprintln(out, "no regressions")
		return
	}
	fmt.Fprintln(out, "SUITE\tCASE\tFROM\tTO\tTIME")
	for _, r := range index.Regressions {
		fmt.Fprintf(out, "%s\t%s\t%s\t%s\t%s\n",
			r.SuiteID, r.CaseID, r.FromRunID, r.ToRunID, r.ToStartedAt.Format("2006-01-02 15:04:05"))
	}
}
