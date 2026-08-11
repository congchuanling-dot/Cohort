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
	case "run-revision-child":
		if os.Getenv(deliveryChildEnv) != "1" {
			return errors.New("deliver run-revision-child is internal; use cohort deliver revise <delivery_id>")
		}
		if len(args) != 4 {
			return errors.New("invalid internal revision worker arguments")
		}
		configPath, err := app.ResolveConfigPath(explicitConfigPath)
		if err != nil {
			return err
		}
		cfg, err := app.LoadConfig(configPath)
		if err != nil {
			return err
		}
		result, err := runDeliveryRevisionChild(ctx, cfg, store, args[1], args[2], args[3])
		if err != nil {
			return err
		}
		return store.SaveWorkerResult(args[1], args[2], args[3], result)
	case "run-verifier-child":
		if os.Getenv(deliveryChildEnv) != "1" {
			return errors.New("deliver run-verifier-child is internal; use cohort deliver verify <delivery_id>")
		}
		if len(args) != 3 {
			return errors.New("invalid internal verifier arguments")
		}
		configPath, err := app.ResolveConfigPath(explicitConfigPath)
		if err != nil {
			return err
		}
		cfg, err := app.LoadConfig(configPath)
		if err != nil {
			return err
		}
		role := delivery.AgentRole(args[2])
		report, err := runDeliveryVerifierChild(ctx, cfg, store, args[1], role)
		if err != nil {
			return err
		}
		integration, err := store.LoadIntegration(args[1])
		if err != nil {
			return err
		}
		return store.SaveVerifierWorkerReport(args[1], integration.TreeHash, role, report)
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
		if err != nil {
			return err
		}
		verification, err := (delivery.VerifierCouncil{Store: store, MaxParallel: 3}).Run(
			ctx,
			args[1],
			deliverySubprocessVerifier(configPath, store),
		)
		printDeliveryVerificationSummary(out, verification)
		if err != nil {
			return err
		}
		if verification.Status == delivery.VerificationFailed {
			return runDeliveryRevisionLoop(ctx, configPath, store, args[1], out)
		}
		return nil
	case "integrate":
		if len(args) != 2 {
			return errors.New("usage: cohort deliver integrate <delivery_id>")
		}
		integration, err := (delivery.Integrator{Store: store}).Run(ctx, args[1])
		printDeliveryIntegrationSummary(out, integration)
		return err
	case "verify":
		if len(args) != 2 {
			return errors.New("usage: cohort deliver verify <delivery_id>")
		}
		configPath, err := app.ResolveConfigPath(explicitConfigPath)
		if err != nil {
			return err
		}
		if _, err := app.LoadConfig(configPath); err != nil {
			return err
		}
		verification, err := (delivery.VerifierCouncil{Store: store, MaxParallel: 3}).Run(
			ctx,
			args[1],
			deliverySubprocessVerifier(configPath, store),
		)
		printDeliveryVerificationSummary(out, verification)
		return err
	case "revise":
		if len(args) != 2 {
			return errors.New("usage: cohort deliver revise <delivery_id>")
		}
		configPath, err := app.ResolveConfigPath(explicitConfigPath)
		if err != nil {
			return err
		}
		if _, err := app.LoadConfig(configPath); err != nil {
			return err
		}
		return runDeliveryRevisionLoop(ctx, configPath, store, args[1], out)
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
		if verification, verificationErr := store.LoadVerification(item.ID); verificationErr == nil {
			printDeliveryVerificationSummary(out, verification)
		}
		if revisions, revisionErr := store.LoadRevisions(item.ID); revisionErr == nil && len(revisions.Records) > 0 {
			printDeliveryRevisionSummary(out, revisions.Records[len(revisions.Records)-1])
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

func deliverySubprocessVerifier(configPath string, store delivery.Store) delivery.SemanticVerifier {
	return func(ctx context.Context, item delivery.Delivery, _ delivery.AcceptanceContract, integration delivery.IntegrationState, role delivery.AgentRole) (delivery.VerifierReport, error) {
		executable, err := os.Executable()
		if err != nil {
			return delivery.VerifierReport{}, err
		}
		args := []string{
			"--config", configPath,
			"deliver", "run-verifier-child",
			item.ID, string(role),
		}
		command := exec.CommandContext(ctx, executable, args...)
		command.Dir = item.ProjectRoot
		command.Env = append(os.Environ(), deliveryChildEnv+"=1")
		output, err := command.CombinedOutput()
		if err != nil {
			return delivery.VerifierReport{}, fmt.Errorf("isolated %s failed: %w: %s", role, err, strings.TrimSpace(string(output)))
		}
		return store.LoadVerifierWorkerReport(item.ID, integration.TreeHash, role)
	}
}

func runDeliveryVerifierChild(ctx context.Context, cfg app.Config, store delivery.Store, deliveryID string, role delivery.AgentRole) (delivery.VerifierReport, error) {
	item, err := store.Load(deliveryID)
	if err != nil {
		return delivery.VerifierReport{}, err
	}
	contract, _, err := store.LoadPlan(deliveryID)
	if err != nil {
		return delivery.VerifierReport{}, err
	}
	integration, err := store.LoadIntegration(deliveryID)
	if err != nil {
		return delivery.VerifierReport{}, err
	}
	_, diff, err := store.ReadArtifact(deliveryID, integration.DiffArtifact)
	if err != nil {
		return delivery.VerifierReport{}, err
	}
	diffText := string(diff)
	diffRunes := []rune(diffText)
	if len(diffRunes) > 120000 {
		diffText = string(diffRunes[:120000]) + "\n...[diff truncated; inspect files for full context]..."
	}
	prompt, err := delivery.VerifierTaskPrompt(item, contract, integration, diffText, role)
	if err != nil {
		return delivery.VerifierReport{}, err
	}
	runCfg := cfg
	runCfg.Workspace = integration.WorktreePath
	runCfg.LogDir = filepath.Join(store.RootDir, deliveryID, "verifier-logs", string(role))
	runCfg.Tools.EnabledGroups = []string{"core", "lsp"}
	runCfg.Tools.AdaptiveRouting = false
	runCfg.Observability.AutoRefresh = false
	if runCfg.MaxTurns <= 0 || runCfg.MaxTurns > 60 {
		runCfg.MaxTurns = 60
	}
	runner, err := app.NewRunner(runCfg)
	if err != nil {
		return delivery.VerifierReport{}, err
	}
	defer runner.Close()
	runner.Tools = explorer.NewReadOnlyToolRunner(runner.Tools, integration.WorktreePath)
	runner.SessionStore = nil
	runner.SessionCWD = integration.WorktreePath
	runner.RunMode = agent.RunModeDeliveryVerifier
	runner.DisableLongTermMemoryReview = true
	runner.DisableCapabilityGapRecording = true
	runner.SystemPrompt = delivery.VerifierSystemPrompt(role, cfg.Language)
	verifyCtx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	result, err := runner.Run(verifyCtx, prompt, agent.NewConsoleSink(io.Discard))
	if err != nil {
		return delivery.VerifierReport{}, err
	}
	if result.Status != agent.RunStatusDone || result.Response == nil {
		return delivery.VerifierReport{}, fmt.Errorf("%s finished with status %s", role, result.Status)
	}
	report, err := delivery.ParseVerifierReport(result.Response.Content)
	if err != nil {
		return delivery.VerifierReport{}, err
	}
	report.Role = role
	return report, nil
}

func runDeliveryRevisionLoop(ctx context.Context, configPath string, store delivery.Store, deliveryID string, out io.Writer) error {
	for {
		record, err := (delivery.RevisionService{Store: store}).Run(
			ctx,
			deliveryID,
			deliveryRevisionSubprocessWorker(configPath, store),
		)
		printDeliveryRevisionSummary(out, record)
		if err != nil {
			return err
		}
		integration, err := (delivery.Integrator{Store: store}).Run(ctx, deliveryID)
		printDeliveryIntegrationSummary(out, integration)
		if err != nil {
			return err
		}
		verification, err := (delivery.VerifierCouncil{Store: store, MaxParallel: 3}).Run(
			ctx,
			deliveryID,
			deliverySubprocessVerifier(configPath, store),
		)
		printDeliveryVerificationSummary(out, verification)
		if err != nil {
			return err
		}
		if verification.Status != delivery.VerificationFailed {
			return nil
		}
	}
}

func deliveryRevisionSubprocessWorker(configPath string, store delivery.Store) delivery.NodeWorker {
	return func(ctx context.Context, item delivery.Delivery, _ delivery.AcceptanceContract, node delivery.TaskNode, candidate delivery.Candidate) (delivery.WorkerResult, error) {
		executable, err := os.Executable()
		if err != nil {
			return delivery.WorkerResult{}, err
		}
		args := []string{
			"--config", configPath,
			"deliver", "run-revision-child",
			item.ID, node.ID, candidate.ID,
		}
		command := exec.CommandContext(ctx, executable, args...)
		command.Dir = item.ProjectRoot
		command.Env = append(os.Environ(), deliveryChildEnv+"=1")
		output, err := command.CombinedOutput()
		if err != nil {
			return delivery.WorkerResult{}, fmt.Errorf("isolated revision worker failed: %w: %s", err, strings.TrimSpace(string(output)))
		}
		return store.LoadWorkerResult(item.ID, node.ID, candidate.ID)
	}
}

func runDeliveryRevisionChild(ctx context.Context, cfg app.Config, store delivery.Store, deliveryID string, nodeID string, candidateID string) (delivery.WorkerResult, error) {
	item, err := store.Load(deliveryID)
	if err != nil {
		return delivery.WorkerResult{}, err
	}
	contract, _, err := store.LoadPlan(deliveryID)
	if err != nil {
		return delivery.WorkerResult{}, err
	}
	revisions, err := store.LoadRevisions(deliveryID)
	if err != nil {
		return delivery.WorkerResult{}, err
	}
	for _, record := range revisions.Records {
		if record.Node.ID == nodeID && record.Candidate.ID == candidateID {
			return executeDeliveryBuilder(ctx, cfg, store, item, contract, record.Node, record.Candidate, "")
		}
	}
	return delivery.WorkerResult{}, fmt.Errorf("revision candidate %q not found", candidateID)
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
	return executeDeliveryBuilder(ctx, cfg, store, item, contract, node, candidate, ownerID)
}

func executeDeliveryBuilder(ctx context.Context, cfg app.Config, store delivery.Store, item delivery.Delivery, contract delivery.AcceptanceContract, node delivery.TaskNode, candidate delivery.Candidate, ownerID string) (delivery.WorkerResult, error) {
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
	runCfg.LogDir = filepath.Join(store.RootDir, item.ID, "worker-logs", node.ID, candidate.ID)
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
	if ownerID != "" {
		go heartbeatDeliveryChild(workerCtx, store, item.ID, node.ID, ownerID, stopHeartbeat)
	}
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
	commit, err := manager.Commit(ctx, spec, "cohort delivery "+item.ID+" "+node.ID)
	if err != nil {
		return delivery.WorkerResult{}, err
	}
	responseSummary := strings.TrimSpace(result.Response.Content)
	payload, _ := json.MarshalIndent(map[string]any{
		"delivery_id":  item.ID,
		"node_id":      node.ID,
		"candidate_id": candidate.ID,
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

func printDeliveryVerificationSummary(out io.Writer, state delivery.VerificationState) {
	if state.DeliveryID == "" {
		return
	}
	fmt.Fprintf(out, "verification_status: %s\n", state.Status)
	fmt.Fprintf(out, "verification_round: %d\n", state.Round)
	fmt.Fprintf(out, "verifier_reports: %d\n", len(state.Reports))
	fmt.Fprintf(out, "verifier_findings: %d\n", len(state.Findings))
	blocking := 0
	for _, finding := range state.Findings {
		if finding.Status == delivery.FindingOpen &&
			(finding.Severity == delivery.SeverityHigh || finding.Severity == delivery.SeverityCritical) {
			blocking++
		}
	}
	fmt.Fprintf(out, "blocking_findings: %d\n", blocking)
	if state.Error != "" {
		fmt.Fprintf(out, "verification_error: %s\n", state.Error)
	}
}

func printDeliveryRevisionSummary(out io.Writer, record delivery.RevisionRecord) {
	if record.DeliveryID == "" {
		return
	}
	fmt.Fprintf(out, "revision_round: %d\n", record.Round)
	fmt.Fprintf(out, "revision_status: %s\n", record.Status)
	fmt.Fprintf(out, "revision_findings: %d\n", len(record.FindingIDs))
	if record.Candidate.Commit != "" {
		fmt.Fprintf(out, "revision_commit: %s\n", record.Candidate.Commit)
		fmt.Fprintf(out, "revision_tree: %s\n", record.Candidate.TreeHash)
	}
	if record.Error != "" {
		fmt.Fprintf(out, "revision_error: %s\n", record.Error)
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
