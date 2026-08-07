package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"cohort/internal/app"
	"cohort/internal/evaluation"
	"cohort/internal/hermes"
)

func hermesJobs(ctx context.Context, root string, cfg app.Config, store hermes.Store, args []string, out io.Writer) error {
	if len(args) == 0 || args[0] == "list" {
		jobs, err := store.LoadJobs()
		if err != nil {
			return err
		}
		w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tENABLED\tSUITE\tSCHEDULE\tNEXT\tLAST\tFAILURES")
		for _, job := range jobs.Jobs {
			schedule := job.Schedule.Cron
			if schedule == "" {
				schedule = (time.Duration(job.Schedule.IntervalSeconds) * time.Second).String()
			}
			fmt.Fprintf(w, "%s\t%t\t%s\t%s\t%s\t%s\t%d\n",
				job.ID, job.Enabled, job.Suite, schedule, formatHermesTime(job.NextRunAt), job.LastStatus, job.ConsecutiveFailures)
		}
		return w.Flush()
	}
	switch args[0] {
	case "init":
		runNow := false
		for _, arg := range args[1:] {
			if arg == "--run-now" {
				runNow = true
			} else {
				return fmt.Errorf("unknown hermes jobs init option %q", arg)
			}
		}
		defaults := []hermes.Job{
			{
				ID: "core-regression", Enabled: true, Suite: "core", Repeat: 1, Workers: 2,
				Schedule: hermes.Schedule{IntervalSeconds: 6 * 60 * 60}, Retry: hermes.Retry{MaxAttempts: 2, BackoffSeconds: 60},
				Gate: hermes.Gate{MinScore: 85, MinPassRate: 90, MinStability: 90, MaxRegressions: 0},
			},
			{
				ID: "stateful-stability", Enabled: true, Suite: "stateful", Repeat: 3, Workers: 2, Judge: "llm",
				Schedule: hermes.Schedule{IntervalSeconds: 12 * 60 * 60}, Retry: hermes.Retry{MaxAttempts: 2, BackoffSeconds: 120},
				Gate: hermes.Gate{MinScore: 80, MinPassRate: 90, MinStability: 90, MaxRegressions: 0},
			},
			{
				ID: "tool-routing-daily", Enabled: true, Suite: "tool-routing", Repeat: 1, Workers: 1,
				Schedule: hermes.Schedule{Cron: "0 9 * * *"}, Retry: hermes.Retry{MaxAttempts: 2, BackoffSeconds: 60},
				Gate: hermes.Gate{MinScore: 80, MinPassRate: 75, MaxRegressions: 0},
			},
		}
		for _, job := range defaults {
			if _, err := hermes.FindJob(store, job.ID); err == nil {
				continue
			}
			created, err := hermes.UpsertJob(store, job)
			if err != nil {
				return err
			}
			if runNow {
				jobs, err := store.LoadJobs()
				if err != nil {
					return err
				}
				for i := range jobs.Jobs {
					if jobs.Jobs[i].ID == created.ID {
						jobs.Jobs[i].NextRunAt = time.Now().UTC()
					}
				}
				if err := store.SaveJobs(jobs); err != nil {
					return err
				}
			}
			fmt.Fprintf(out, "created: %s\n", created.ID)
		}
		return nil
	case "add":
		job, err := parseHermesJob(args[1:])
		if err != nil {
			return err
		}
		if _, err := hermes.FindJob(store, job.ID); err == nil {
			return fmt.Errorf("job %q already exists", job.ID)
		}
		evalStore := evaluation.NewStore(root)
		if _, err := evaluation.LoadSuite(evalStore.SuitePath(job.Suite)); err != nil {
			return fmt.Errorf("load job suite: %w", err)
		}
		job, err = hermes.UpsertJob(store, job)
		if err != nil {
			return err
		}
		return printHermesJob(job, out)
	case "show":
		if len(args) != 2 {
			return errors.New("usage: cohort hermes jobs show <id>")
		}
		job, err := hermes.FindJob(store, args[1])
		if err != nil {
			return err
		}
		return printHermesJob(job, out)
	case "enable", "disable":
		if len(args) != 2 {
			return fmt.Errorf("usage: cohort hermes jobs %s <id>", args[0])
		}
		job, err := hermes.SetJobEnabled(store, args[1], args[0] == "enable")
		if err != nil {
			return err
		}
		return printHermesJob(job, out)
	case "remove":
		if len(args) != 2 {
			return errors.New("usage: cohort hermes jobs remove <id>")
		}
		return hermes.RemoveJob(store, args[1])
	case "run":
		if len(args) != 2 {
			return errors.New("usage: cohort hermes jobs run <id>")
		}
		service, err := hermes.NewService(root)
		if err != nil {
			return err
		}
		configureHermesEvalRunner(service, cfg, out)
		job, err := service.RunJob(ctx, args[1])
		if err != nil {
			return err
		}
		return printHermesJob(job, out)
	default:
		return fmt.Errorf("unknown hermes jobs command %q", args[0])
	}
}

func parseHermesJob(args []string) (hermes.Job, error) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return hermes.Job{}, errors.New("usage: cohort hermes jobs add <id> --suite <id> (--interval <duration>|--cron <expr>) [options]")
	}
	job := hermes.Job{
		ID:      args[0],
		Enabled: true,
		Repeat:  1,
		Workers: 1,
		Retry:   hermes.Retry{MaxAttempts: 1, BackoffSeconds: 30},
	}
	for i := 1; i < len(args); i++ {
		arg := args[i]
		if arg == "--disabled" {
			job.Enabled = false
			continue
		}
		if i+1 >= len(args) {
			return hermes.Job{}, fmt.Errorf("%s requires a value", arg)
		}
		value := args[i+1]
		i++
		switch arg {
		case "--suite":
			job.Suite = value
		case "--profile":
			job.Profile = value
		case "--judge":
			job.Judge = value
		case "--judge-profile":
			job.JudgeProfile = value
		case "--repeat":
			parsed, err := strconv.Atoi(value)
			if err != nil {
				return hermes.Job{}, fmt.Errorf("invalid --repeat: %w", err)
			}
			job.Repeat = parsed
		case "--workers":
			parsed, err := strconv.Atoi(value)
			if err != nil {
				return hermes.Job{}, fmt.Errorf("invalid --workers: %w", err)
			}
			job.Workers = parsed
		case "--interval":
			duration, err := time.ParseDuration(value)
			if err != nil || duration < time.Second {
				return hermes.Job{}, errors.New("--interval must be a valid duration such as 15m or 2h")
			}
			job.Schedule.IntervalSeconds = int(duration / time.Second)
		case "--cron":
			job.Schedule.Cron = value
		case "--max-attempts":
			parsed, err := strconv.Atoi(value)
			if err != nil {
				return hermes.Job{}, fmt.Errorf("invalid --max-attempts: %w", err)
			}
			job.Retry.MaxAttempts = parsed
		case "--backoff":
			duration, err := time.ParseDuration(value)
			if err != nil || duration < 0 {
				return hermes.Job{}, errors.New("--backoff must be a valid duration")
			}
			job.Retry.BackoffSeconds = int(duration / time.Second)
		case "--min-score":
			parsed, err := parsePercentValue(value, arg)
			if err != nil {
				return hermes.Job{}, err
			}
			job.Gate.MinScore = parsed
		case "--min-pass-rate":
			parsed, err := parsePercentValue(value, arg)
			if err != nil {
				return hermes.Job{}, err
			}
			job.Gate.MinPassRate = parsed
		case "--min-stability":
			parsed, err := parsePercentValue(value, arg)
			if err != nil {
				return hermes.Job{}, err
			}
			job.Gate.MinStability = parsed
		case "--max-regressions":
			parsed, err := strconv.Atoi(value)
			if err != nil {
				return hermes.Job{}, fmt.Errorf("invalid --max-regressions: %w", err)
			}
			job.Gate.MaxRegressions = parsed
		default:
			return hermes.Job{}, fmt.Errorf("unknown hermes job option %q", arg)
		}
	}
	if err := hermes.ValidateJob(job); err != nil {
		return hermes.Job{}, err
	}
	return job, nil
}

func configureHermesEvalRunner(service *hermes.Service, cfg app.Config, out io.Writer) {
	service.Output = out
	service.EvalRunner = func(ctx context.Context, job hermes.Job) (hermes.EvalRunOutcome, error) {
		before, err := service.EvalStore.ListResults()
		if err != nil {
			return hermes.EvalRunOutcome{}, err
		}
		known := map[string]bool{}
		for _, result := range before {
			known[result.RunID] = true
		}
		opts := evalRunOptions{
			SuitePath:    job.Suite,
			Workers:      job.Workers,
			Repeat:       job.Repeat,
			JudgeMode:    job.Judge,
			JudgeProfile: job.JudgeProfile,
			Gate: evaluation.GateConfig{
				MinScore:       job.Gate.MinScore,
				MinPassRate:    job.Gate.MinPassRate,
				MinStability:   job.Gate.MinStability,
				MaxRegressions: job.Gate.MaxRegressions,
			},
		}
		if job.Profile != "" {
			opts.Profiles = []string{job.Profile}
		}
		runErr := executeEvalRun(ctx, cfg, service.EvalStore, opts, out)
		after, listErr := service.EvalStore.ListResults()
		if listErr != nil {
			return hermes.EvalRunOutcome{}, listErr
		}
		outcome := hermes.EvalRunOutcome{GatePassed: true}
		for _, result := range after {
			if known[result.RunID] {
				continue
			}
			outcome.RunIDs = append(outcome.RunIDs, result.RunID)
			if result.Gate == nil || !result.Gate.Passed {
				outcome.GatePassed = false
			}
		}
		if len(outcome.RunIDs) == 0 {
			outcome.GatePassed = false
			if runErr == nil {
				runErr = errors.New("eval runner produced no persisted result")
			}
		}
		return outcome, runErr
	}
}

func runHermesActionVerification(ctx context.Context, root string, cfg app.Config, store hermes.Store, id string, out io.Writer) (hermes.QueueAction, error) {
	queue, err := store.LoadQueue()
	if err != nil {
		return hermes.QueueAction{}, err
	}
	var target *hermes.QueueAction
	for i := range queue.Actions {
		if queue.Actions[i].ID == id || queue.Actions[i].Fingerprint == id {
			target = &queue.Actions[i]
			break
		}
	}
	if target == nil {
		return hermes.QueueAction{}, fmt.Errorf("action %q not found", id)
	}
	if target.SuiteID == "" || target.CaseID == "" {
		return hermes.QueueAction{}, errors.New("action does not identify a verifiable suite and case")
	}
	evalStore := evaluation.NewStore(root)
	before, err := evalStore.ListResults()
	if err != nil {
		return hermes.QueueAction{}, err
	}
	known := map[string]bool{}
	for _, result := range before {
		known[result.RunID] = true
	}
	opts := evalRunOptions{
		SuitePath: target.SuiteID,
		CaseIDs:   []string{target.CaseID},
		Workers:   1,
		Repeat:    2,
		Gate: evaluation.GateConfig{
			MinScore:       80,
			MinPassRate:    100,
			MinStability:   100,
			MaxRegressions: 0,
		},
	}
	runErr := executeEvalRun(ctx, cfg, evalStore, opts, out)
	after, err := evalStore.ListResults()
	if err != nil {
		return hermes.QueueAction{}, err
	}
	for _, result := range after {
		if known[result.RunID] || result.SuiteID != target.SuiteID {
			continue
		}
		action, verifyErr := hermes.VerifyActionWithRun(store, evalStore, id, result.RunID, false)
		if verifyErr != nil {
			if runErr != nil {
				return hermes.QueueAction{}, fmt.Errorf("%v; verification: %w", runErr, verifyErr)
			}
			return hermes.QueueAction{}, verifyErr
		}
		return action, nil
	}
	if runErr != nil {
		return hermes.QueueAction{}, runErr
	}
	return hermes.QueueAction{}, errors.New("verification run produced no result")
}

func printHermesJob(job hermes.Job, out io.Writer) error {
	data, err := json.MarshalIndent(job, "", "  ")
	if err != nil {
		return err
	}
	fmt.Fprintln(out, string(data))
	return nil
}

func formatHermesTime(value time.Time) string {
	if value.IsZero() {
		return "-"
	}
	return value.Local().Format(time.RFC3339)
}
