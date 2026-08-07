package hermes

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

func normalizeJob(job *Job) {
	job.ID = strings.TrimSpace(job.ID)
	job.Suite = strings.TrimSpace(job.Suite)
	job.Profile = strings.TrimSpace(job.Profile)
	job.Judge = strings.ToLower(strings.TrimSpace(job.Judge))
	job.JudgeProfile = strings.TrimSpace(job.JudgeProfile)
	job.Schedule.Cron = strings.TrimSpace(job.Schedule.Cron)
	if job.Repeat <= 0 {
		job.Repeat = 1
	}
	if job.Workers <= 0 {
		job.Workers = 1
	}
	if job.Retry.MaxAttempts <= 0 {
		job.Retry.MaxAttempts = 1
	}
	if job.Retry.BackoffSeconds < 0 {
		job.Retry.BackoffSeconds = 0
	}
	if job.Retry.MaxAttempts > 1 && job.Retry.BackoffSeconds == 0 {
		job.Retry.BackoffSeconds = 30
	}
}

func ValidateJob(job Job) error {
	normalizeJob(&job)
	if job.ID == "" {
		return errors.New("job id is required")
	}
	if job.Suite == "" {
		return errors.New("job suite is required")
	}
	if job.Repeat < 1 || job.Repeat > 20 {
		return errors.New("job repeat must be between 1 and 20")
	}
	if job.Workers < 1 || job.Workers > 16 {
		return errors.New("job workers must be between 1 and 16")
	}
	if job.Judge != "" && job.Judge != "off" && job.Judge != "heuristic" && job.Judge != "llm" {
		return errors.New("job judge must be off, heuristic, or llm")
	}
	if job.Schedule.IntervalSeconds > 0 && job.Schedule.Cron != "" {
		return errors.New("job schedule must use either interval_seconds or cron")
	}
	if job.Schedule.IntervalSeconds <= 0 && job.Schedule.Cron == "" {
		return errors.New("job schedule requires interval_seconds or cron")
	}
	if job.Schedule.IntervalSeconds > 0 && job.Schedule.IntervalSeconds < 10 {
		return errors.New("job interval_seconds must be at least 10")
	}
	if job.Schedule.Cron != "" {
		if _, err := parseCron(job.Schedule.Cron); err != nil {
			return err
		}
	}
	if job.Retry.MaxAttempts < 1 || job.Retry.MaxAttempts > 10 {
		return errors.New("job retry.max_attempts must be between 1 and 10")
	}
	for name, value := range map[string]float64{
		"min_score": job.Gate.MinScore, "min_pass_rate": job.Gate.MinPassRate, "min_stability": job.Gate.MinStability,
	} {
		if value < 0 || value > 100 {
			return fmt.Errorf("job gate.%s must be between 0 and 100", name)
		}
	}
	if job.Gate.MaxRegressions < 0 {
		return errors.New("job gate.max_regressions must be >= 0")
	}
	return nil
}

func UpsertJob(store Store, job Job) (Job, error) {
	normalizeJob(&job)
	if err := ValidateJob(job); err != nil {
		return Job{}, err
	}
	jobs, err := store.LoadJobs()
	if err != nil {
		return Job{}, err
	}
	now := time.Now().UTC()
	for i := range jobs.Jobs {
		if jobs.Jobs[i].ID != job.ID {
			continue
		}
		job.CreatedAt = jobs.Jobs[i].CreatedAt
		if job.CreatedAt.IsZero() {
			job.CreatedAt = now
		}
		job.UpdatedAt = now
		if job.NextRunAt.IsZero() {
			job.NextRunAt, err = NextJobRun(job, now)
			if err != nil {
				return Job{}, err
			}
		}
		jobs.Jobs[i] = job
		if err := store.SaveJobs(jobs); err != nil {
			return Job{}, err
		}
		return job, nil
	}
	job.CreatedAt = now
	job.UpdatedAt = now
	job.NextRunAt, err = NextJobRun(job, now)
	if err != nil {
		return Job{}, err
	}
	jobs.Jobs = append(jobs.Jobs, job)
	if err := store.SaveJobs(jobs); err != nil {
		return Job{}, err
	}
	return job, nil
}

func SetJobEnabled(store Store, id string, enabled bool) (Job, error) {
	jobs, err := store.LoadJobs()
	if err != nil {
		return Job{}, err
	}
	for i := range jobs.Jobs {
		if jobs.Jobs[i].ID != id {
			continue
		}
		jobs.Jobs[i].Enabled = enabled
		jobs.Jobs[i].UpdatedAt = time.Now().UTC()
		if enabled {
			jobs.Jobs[i].NextRunAt, err = NextJobRun(jobs.Jobs[i], time.Now().UTC())
			if err != nil {
				return Job{}, err
			}
		}
		if err := store.SaveJobs(jobs); err != nil {
			return Job{}, err
		}
		return jobs.Jobs[i], nil
	}
	return Job{}, fmt.Errorf("job %q not found", id)
}

func RemoveJob(store Store, id string) error {
	jobs, err := store.LoadJobs()
	if err != nil {
		return err
	}
	for i := range jobs.Jobs {
		if jobs.Jobs[i].ID == id {
			jobs.Jobs = append(jobs.Jobs[:i], jobs.Jobs[i+1:]...)
			return store.SaveJobs(jobs)
		}
	}
	return fmt.Errorf("job %q not found", id)
}

func FindJob(store Store, id string) (Job, error) {
	jobs, err := store.LoadJobs()
	if err != nil {
		return Job{}, err
	}
	for _, job := range jobs.Jobs {
		if job.ID == id {
			return job, nil
		}
	}
	return Job{}, fmt.Errorf("job %q not found", id)
}

func NextJobRun(job Job, after time.Time) (time.Time, error) {
	normalizeJob(&job)
	if job.Schedule.IntervalSeconds > 0 {
		base := after
		if !job.LastRunAt.IsZero() && job.LastRunAt.After(base) {
			base = job.LastRunAt
		}
		return base.Add(time.Duration(job.Schedule.IntervalSeconds) * time.Second).UTC(), nil
	}
	spec, err := parseCron(job.Schedule.Cron)
	if err != nil {
		return time.Time{}, err
	}
	next := after.UTC().Truncate(time.Minute).Add(time.Minute)
	limit := next.AddDate(1, 0, 0)
	for next.Before(limit) {
		if spec.matches(next) {
			return next, nil
		}
		next = next.Add(time.Minute)
	}
	return time.Time{}, fmt.Errorf("cron %q has no matching time within one year", job.Schedule.Cron)
}

type cronSpec struct {
	minute             map[int]bool
	hour               map[int]bool
	dayOfMonth         map[int]bool
	month              map[int]bool
	dayOfWeek          map[int]bool
	dayOfMonthWildcard bool
	dayOfWeekWildcard  bool
}

func parseCron(expression string) (cronSpec, error) {
	fields := strings.Fields(expression)
	if len(fields) != 5 {
		return cronSpec{}, fmt.Errorf("cron must contain five fields: minute hour day-of-month month day-of-week")
	}
	ranges := [][2]int{{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 6}}
	values := make([]map[int]bool, 5)
	for i, field := range fields {
		parsed, err := parseCronField(field, ranges[i][0], ranges[i][1])
		if err != nil {
			return cronSpec{}, fmt.Errorf("invalid cron field %q: %w", field, err)
		}
		values[i] = parsed
	}
	return cronSpec{
		minute: values[0], hour: values[1], dayOfMonth: values[2], month: values[3], dayOfWeek: values[4],
		dayOfMonthWildcard: fields[2] == "*", dayOfWeekWildcard: fields[4] == "*",
	}, nil
}

func parseCronField(field string, min int, max int) (map[int]bool, error) {
	result := map[int]bool{}
	for _, part := range strings.Split(field, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, errors.New("empty list item")
		}
		step := 1
		base := part
		if strings.Contains(part, "/") {
			pieces := strings.Split(part, "/")
			if len(pieces) != 2 {
				return nil, errors.New("invalid step")
			}
			base = pieces[0]
			parsedStep, err := strconv.Atoi(pieces[1])
			if err != nil || parsedStep <= 0 {
				return nil, errors.New("step must be positive")
			}
			step = parsedStep
		}
		start, end := min, max
		if base != "*" {
			if strings.Contains(base, "-") {
				pieces := strings.Split(base, "-")
				if len(pieces) != 2 {
					return nil, errors.New("invalid range")
				}
				var err error
				start, err = strconv.Atoi(pieces[0])
				if err != nil {
					return nil, errors.New("invalid range start")
				}
				end, err = strconv.Atoi(pieces[1])
				if err != nil {
					return nil, errors.New("invalid range end")
				}
			} else {
				value, err := strconv.Atoi(base)
				if err != nil {
					return nil, errors.New("not a number")
				}
				start, end = value, value
			}
		}
		if start < min || end > max || start > end {
			return nil, fmt.Errorf("value must be between %d and %d", min, max)
		}
		for value := start; value <= end; value += step {
			result[value] = true
		}
	}
	return result, nil
}

func (s cronSpec) matches(value time.Time) bool {
	dayMatches := false
	switch {
	case s.dayOfMonthWildcard && s.dayOfWeekWildcard:
		dayMatches = true
	case s.dayOfMonthWildcard:
		dayMatches = s.dayOfWeek[int(value.Weekday())]
	case s.dayOfWeekWildcard:
		dayMatches = s.dayOfMonth[value.Day()]
	default:
		dayMatches = s.dayOfMonth[value.Day()] || s.dayOfWeek[int(value.Weekday())]
	}
	return s.minute[value.Minute()] &&
		s.hour[value.Hour()] &&
		s.month[int(value.Month())] &&
		dayMatches
}
