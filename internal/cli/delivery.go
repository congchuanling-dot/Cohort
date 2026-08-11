package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"cohort/internal/agent"
	"cohort/internal/app"
	"cohort/internal/delivery"
	"cohort/internal/explorer"
	"cohort/internal/worktree"
)

const deliveryChildEnv = "COHORT_DELIVERY_CHILD"

func runDeliveryCommand(ctx context.Context, explicitConfigPath string, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New(`usage: cohort deliver plan "requirement" | run <id> | list | status [id] | show <id> | cancel <id>`)
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
	case "run-node-child":
		if os.Getenv(deliveryChildEnv) != "1" {
			return errors.New("deliver run-node-child is internal; use cohort deliver run <delivery_id>")
		}
		if len(args) != 5 || args[4] == "" {
			return errors.New("invalid internal delivery worker arguments")
		}
		configPath, err := app.ResolveConfigPath(explicitConfigPath)
		if err != nil {
			return err
		}
		cfg, err := app.LoadConfig(configPath)
		if err != nil {
			return err
		}
		result, err := runDeliveryBuilderChild(ctx, cfg, store, args[1], args[2], args[3], args[4])
		if err != nil {
			return err
		}
		return store.SaveWorkerResult(args[1], args[2], args[3], result)
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
	case "run":
		if len(args) != 2 {
			return errors.New("usage: cohort deliver run <delivery_id>")
		}
		configPath, err := app.ResolveConfigPath(explicitConfigPath)
		if err != nil {
			return err
		}
		if _, err := app.LoadConfig(configPath); err != nil {
			return err
		}
		ownerID := fmt.Sprintf("delivery-scheduler-%d", os.Getpid())
		scheduler := delivery.Scheduler{
			Store:       store,
			LeaseTTL:    45 * time.Second,
			MaxParallel: 3,
			OwnerID:     ownerID,
		}
		fmt.Fprintf(out, "running: %s\n", args[1])
		runtime, err := scheduler.Run(ctx, args[1], deliverySubprocessWorker(configPath, store, ownerID))
		printDeliveryRuntimeSummary(out, runtime)
		if err != nil {
			return err
		}
		integration, err := (delivery.Integrator{Store: store}).Run(ctx, args[1])
		printDeliveryIntegrationSummary(out, integration)
		return err
	case "integrate":
		if len(args) != 2 {
			return errors.New("usage: cohort deliver integrate <delivery_id>")
		}
		integration, err := (delivery.Integrator{Store: store}).Run(ctx, args[1])
		printDeliveryIntegrationSummary(out, integration)
		return err
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
		if runtime, runtimeErr := store.LoadRuntime(item.ID); runtimeErr == nil {
			printDeliveryRuntimeSummary(out, runtime)
		}
		if integration, integrationErr := store.LoadIntegration(item.ID); integrationErr == nil {
			printDeliveryIntegrationSummary(out, integration)
		}
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

func deliverySubprocessWorker(configPath string, store delivery.Store, ownerID string) delivery.NodeWorker {
	return func(ctx context.Context, item delivery.Delivery, _ delivery.AcceptanceContract, node delivery.TaskNode, candidate delivery.Candidate) (delivery.WorkerResult, error) {
		executable, err := os.Executable()
		if err != nil {
			return delivery.WorkerResult{}, err
		}
		args := []string{
			"--config", configPath,
			"deliver", "run-node-child",
			item.ID, node.ID, candidate.ID, ownerID,
		}
		command := exec.CommandContext(ctx, executable, args...)
		command.Dir = item.ProjectRoot
		command.Env = append(os.Environ(), deliveryChildEnv+"=1")
		output, err := command.CombinedOutput()
		if err != nil {
			return delivery.WorkerResult{}, fmt.Errorf("isolated delivery worker failed: %w: %s", err, strings.TrimSpace(string(output)))
		}
		return store.LoadWorkerResult(item.ID, node.ID, candidate.ID)
	}
}

func runDeliveryBuilderChild(ctx context.Context, cfg app.Config, store delivery.Store, deliveryID string, nodeID string, candidateID string, ownerID string) (delivery.WorkerResult, error) {
	item, err := store.Load(deliveryID)
	if err != nil {
		return delivery.WorkerResult{}, err
	}
	contract, graph, err := store.LoadPlan(deliveryID)
	if err != nil {
		return delivery.WorkerResult{}, err
	}
	runtime, err := store.LoadRuntime(deliveryID)
	if err != nil {
		return delivery.WorkerResult{}, err
	}
	node, candidate, err := findDeliveryNodeCandidate(graph, runtime, nodeID, candidateID)
	if err != nil {
		return delivery.WorkerResult{}, err
	}
	manager, err := worktree.NewManager(item.ProjectRoot, store.WorktreeDir)
	if err != nil {
		return delivery.WorkerResult{}, err
	}
	spec := worktree.Spec{
		ID:         candidate.ID,
		BaseCommit: candidate.BaseCommit,
		Branch:     candidate.Branch,
		Path:       candidate.WorktreePath,
	}
	if err := manager.Prepare(ctx, spec); err != nil {
		return delivery.WorkerResult{}, err
	}
	if err := manager.MergeCommits(ctx, spec, candidate.DependencyCommits); err != nil {
		return delivery.WorkerResult{}, err
	}
	taskPackage := delivery.BuildBuilderTaskPackage(item, contract, node, candidate)
	taskPrompt, err := delivery.BuilderTaskPrompt(taskPackage)
	if err != nil {
		return delivery.WorkerResult{}, err
	}
	runCfg := cfg
	runCfg.Workspace = candidate.WorktreePath
	runCfg.LogDir = filepath.Join(store.RootDir, deliveryID, "worker-logs", nodeID, candidateID)
	runCfg.Tools.EnabledGroups = []string{"core", "lsp"}
	runCfg.Tools.AdaptiveRouting = false
	runCfg.Observability.AutoRefresh = false
	runCfg.MaxTurns = node.Budget.MaxTurns
	runner, err := app.NewRunner(runCfg)
	if err != nil {
		return delivery.WorkerResult{}, err
	}
	defer runner.Close()
	runner.SessionStore = nil
	runner.SessionCWD = candidate.WorktreePath
	runner.RunMode = agent.RunModeDeliveryBuilder
	runner.DisableLongTermMemoryReview = true
	runner.DisableCapabilityGapRecording = true
	runner.SystemPrompt = delivery.BuilderSystemPrompt(cfg.Language)

	workerCtx, cancel := context.WithTimeout(ctx, time.Duration(node.Budget.MaxDurationSecond)*time.Second)
	defer cancel()
	stopHeartbeat := make(chan struct{})
	go heartbeatDeliveryChild(workerCtx, store, deliveryID, nodeID, ownerID, stopHeartbeat)
	startedAt := time.Now()
	result, runErr := runner.Run(workerCtx, taskPrompt, agent.NewConsoleSink(io.Discard))
	close(stopHeartbeat)
	if runErr != nil {
		return delivery.WorkerResult{}, runErr
	}
	if result.Status != agent.RunStatusDone || result.Response == nil {
		return delivery.WorkerResult{}, fmt.Errorf("delivery builder finished with status %s", result.Status)
	}
	inspection, err := manager.Inspect(ctx, spec)
	if err != nil {
		return delivery.WorkerResult{}, err
	}
	if inspection.DiffBytes > 2<<20 {
		return delivery.WorkerResult{}, fmt.Errorf("builder diff exceeds 2 MiB: %d bytes", inspection.DiffBytes)
	}
	commit, err := manager.Commit(ctx, spec, "cohort delivery "+deliveryID+" "+nodeID)
	if err != nil {
		return delivery.WorkerResult{}, err
	}
	responseSummary := strings.TrimSpace(result.Response.Content)
	payload, _ := json.MarshalIndent(map[string]any{
		"delivery_id":  deliveryID,
		"node_id":      nodeID,
		"candidate_id": candidateID,
		"summary":      responseSummary,
		"status":       result.Status,
		"usage":        result.Response.Usage,
	}, "", "  ")
	return delivery.WorkerResult{
		Summary:      responseSummary,
		Tokens:       int64(result.Response.Usage.NormalizedTotal()),
		DurationMS:   time.Since(startedAt).Milliseconds(),
		Commit:       commit,
		TreeHash:     inspection.TreeHash,
		ActualWrites: inspection.Files,
		Diff:         inspection.Diff,
		Result:       payload,
	}, nil
}

func heartbeatDeliveryChild(ctx context.Context, store delivery.Store, deliveryID string, nodeID string, ownerID string, stop <-chan struct{}) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-stop:
			return
		case <-ticker.C:
			_ = store.HeartbeatNode(deliveryID, nodeID, ownerID, 45*time.Second)
		}
	}
}

func findDeliveryNodeCandidate(graph delivery.TaskGraph, runtime delivery.RuntimeState, nodeID string, candidateID string) (delivery.TaskNode, delivery.Candidate, error) {
	var selectedNode delivery.TaskNode
	foundNode := false
	for _, node := range graph.Nodes {
		if node.ID == nodeID {
			selectedNode = node
			foundNode = true
			break
		}
	}
	if !foundNode {
		return delivery.TaskNode{}, delivery.Candidate{}, fmt.Errorf("delivery node %q not found", nodeID)
	}
	nodeRuntime, exists := runtime.Nodes[nodeID]
	if !exists {
		return delivery.TaskNode{}, delivery.Candidate{}, fmt.Errorf("delivery node runtime %q not found", nodeID)
	}
	for _, candidate := range nodeRuntime.Candidates {
		if candidate.ID == candidateID {
			return selectedNode, candidate, nil
		}
	}
	return delivery.TaskNode{}, delivery.Candidate{}, fmt.Errorf("delivery candidate %q not found", candidateID)
}

func printDeliveryRuntimeSummary(out io.Writer, runtime delivery.RuntimeState) {
	if runtime.DeliveryID == "" {
		return
	}
	counts := map[delivery.NodeStatus]int{}
	for _, node := range runtime.Nodes {
		counts[node.Status]++
	}
	fmt.Fprintf(out, "runtime_nodes: %d\n", len(runtime.Nodes))
	for _, status := range []delivery.NodeStatus{
		delivery.NodeReady,
		delivery.NodeRunning,
		delivery.NodePassed,
		delivery.NodeFailed,
		delivery.NodeBlocked,
	} {
		if counts[status] > 0 {
			fmt.Fprintf(out, "nodes_%s: %d\n", status, counts[status])
		}
	}
}

func printDeliveryIntegrationSummary(out io.Writer, state delivery.IntegrationState) {
	if state.DeliveryID == "" {
		return
	}
	fmt.Fprintf(out, "integration_status: %s\n", state.Status)
	if state.Commit != "" {
		fmt.Fprintf(out, "integration_commit: %s\n", state.Commit)
		fmt.Fprintf(out, "integration_tree: %s\n", state.TreeHash)
		fmt.Fprintf(out, "integration_gates: %d\n", len(state.GateResults))
		fmt.Fprintf(out, "integration_evidence: %d\n", len(state.EvidenceIDs))
	}
	if state.Error != "" {
		fmt.Fprintf(out, "integration_error: %s\n", state.Error)
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
