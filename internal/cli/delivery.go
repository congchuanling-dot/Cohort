package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"cohort/internal/agent"
	"cohort/internal/app"
	"cohort/internal/delivery"
	"cohort/internal/explorer"
)

func runDeliveryCommand(ctx context.Context, explicitConfigPath string, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New(`usage: cohort deliver plan "requirement" | list | status [id] | show <id> | cancel <id>`)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	projectRoot, err := delivery.RepositoryRoot(ctx, cwd)
	if err != nil {
		return err
	}
	store := delivery.NewStore(projectRoot)

	switch args[0] {
	case "plan":
		if len(args) < 2 {
			return errors.New(`usage: cohort deliver plan "requirement"`)
		}
		configPath, err := app.ResolveConfigPath(explicitConfigPath)
		if err != nil {
			return err
		}
		cfg, err := app.LoadConfig(configPath)
		if err != nil {
			return err
		}
		fmt.Fprintln(out, "planning: inspecting repository and compiling acceptance contract")
		service := delivery.PlanService{Store: store}
		item, contract, graph, planErr := service.Plan(
			ctx,
			strings.Join(args[1:], " "),
			deliveryAgentPlanner(cfg, store),
		)
		if item.ID != "" {
			printDeliveryPlanSummary(out, item, contract, graph)
		}
		return planErr
	case "list":
		if len(args) != 1 {
			return errors.New("usage: cohort deliver list")
		}
		items, err := store.List()
		if err != nil {
			return err
		}
		if len(items) == 0 {
			fmt.Fprintln(out, "no deliveries")
			return nil
		}
		writer := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		fmt.Fprintln(writer, "ID\tSTATUS\tBASE\tUPDATED\tREQUIREMENT")
		for _, item := range items {
			fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\n",
				item.ID,
				item.Status,
				shortCommit(item.BaseCommit),
				item.UpdatedAt.Format("2006-01-02 15:04:05"),
				oneLine(item.Requirement, 72),
			)
		}
		return writer.Flush()
	case "status":
		id := ""
		if len(args) > 2 {
			return errors.New("usage: cohort deliver status [delivery_id]")
		}
		if len(args) == 2 {
			id = args[1]
		}
		item, err := findDelivery(store, id)
		if err != nil {
			return err
		}
		contract, graph, _ := store.LoadPlan(item.ID)
		printDeliveryPlanSummary(out, item, contract, graph)
		return nil
	case "show":
		if len(args) != 2 {
			return errors.New("usage: cohort deliver show <delivery_id>")
		}
		item, err := store.Load(args[1])
		if err != nil {
			return err
		}
		contract, graph, planErr := store.LoadPlan(item.ID)
		payload := struct {
			Delivery delivery.Delivery            `json:"delivery"`
			Contract *delivery.AcceptanceContract `json:"contract,omitempty"`
			Graph    *delivery.TaskGraph          `json:"graph,omitempty"`
		}{Delivery: item}
		if planErr == nil {
			payload.Contract = &contract
			payload.Graph = &graph
		}
		data, err := json.MarshalIndent(payload, "", "  ")
		if err != nil {
			return err
		}
		fmt.Fprintln(out, string(data))
		return nil
	case "cancel":
		if len(args) != 2 {
			return errors.New("usage: cohort deliver cancel <delivery_id>")
		}
		item, err := store.Transition(args[1], delivery.StatusCancelled, "DeliveryCancelled", nil)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "delivery: %s\nstatus: %s\n", item.ID, item.Status)
		return nil
	default:
		return fmt.Errorf("unknown deliver command %q", args[0])
	}
}

func deliveryAgentPlanner(cfg app.Config, store delivery.Store) delivery.PlanGenerator {
	return func(ctx context.Context, request delivery.PlanRequest) (delivery.PlanDraft, error) {
		runCfg := cfg
		runCfg.Workspace = request.ProjectRoot
		runCfg.LogDir = filepath.Join(store.RootDir, request.Delivery.ID, "planner-logs")
		runCfg.Tools.EnabledGroups = []string{"core", "lsp"}
		runCfg.Tools.AdaptiveRouting = false
		runCfg.Observability.AutoRefresh = false
		if runCfg.MaxTurns <= 0 || runCfg.MaxTurns > 80 {
			runCfg.MaxTurns = 80
		}
		runner, err := app.NewRunner(runCfg)
		if err != nil {
			return delivery.PlanDraft{}, err
		}
		defer runner.Close()
		runner.Tools = explorer.NewReadOnlyToolRunner(runner.Tools, request.ProjectRoot)
		runner.SessionStore = nil
		runner.RunMode = agent.RunModeDeliveryPlanner
		runner.DisableLongTermMemoryReview = true
		runner.DisableCapabilityGapRecording = true
		runner.SystemPrompt = delivery.PlanningSystemPrompt(cfg.Language)
		planCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		defer cancel()
		result, err := runner.Run(planCtx, delivery.PlanningTaskPrompt(request), agent.NewConsoleSink(io.Discard))
		if err != nil {
			return delivery.PlanDraft{}, err
		}
		if result.Status != agent.RunStatusDone || result.Response == nil {
			return delivery.PlanDraft{}, fmt.Errorf("delivery planner finished with status %s", result.Status)
		}
		return delivery.ParsePlanDraft(result.Response.Content)
	}
}

func findDelivery(store delivery.Store, id string) (delivery.Delivery, error) {
	if strings.TrimSpace(id) == "" || id == "latest" {
		return store.Latest()
	}
	return store.Load(id)
}

func printDeliveryPlanSummary(out io.Writer, item delivery.Delivery, contract delivery.AcceptanceContract, graph delivery.TaskGraph) {
	fmt.Fprintf(out, "delivery: %s\n", item.ID)
	fmt.Fprintf(out, "status: %s\n", item.Status)
	fmt.Fprintf(out, "base_commit: %s\n", item.BaseCommit)
	fmt.Fprintf(out, "dirty_at_plan: %t\n", item.DirtyAtPlan)
	if item.ContractHash != "" {
		fmt.Fprintf(out, "contract_hash: %s\n", item.ContractHash)
		fmt.Fprintf(out, "graph_hash: %s\n", item.GraphHash)
		fmt.Fprintf(out, "criteria: %d\n", len(contract.Criteria))
		fmt.Fprintf(out, "gates: %d\n", len(contract.RequiredGates))
		fmt.Fprintf(out, "nodes: %d\n", len(graph.Nodes))
		blocking := 0
		for _, question := range contract.Questions {
			if question.Blocking {
				blocking++
			}
		}
		fmt.Fprintf(out, "blocking_questions: %d\n", blocking)
	}
	if item.Error != "" {
		fmt.Fprintf(out, "error: %s\n", item.Error)
	}
	fmt.Fprintf(out, "state_dir: %s\n", filepath.Join(item.ProjectRoot, ".cohort", "deliveries", item.ID))
}

func shortCommit(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 12 {
		return value[:12]
	}
	return value
}

func oneLine(value string, limit int) string {
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if limit > 0 && len(runes) > limit {
		return string(runes[:limit]) + "..."
	}
	return value
}
