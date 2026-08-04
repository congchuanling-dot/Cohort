package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"cohort/internal/capability"
)

func runCapabilityDepsCommand(store capability.Store, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: cohort capability deps plan|approve|install|list ...")
	}
	switch args[0] {
	case "plan":
		if len(args) != 2 {
			return errors.New("usage: cohort capability deps plan <proposal_id>")
		}
		return planCapabilityDeps(store, args[1], out)
	case "approve":
		if len(args) != 2 {
			return errors.New("usage: cohort capability deps approve <plan_id>")
		}
		return approveCapabilityDeps(store, args[1], out)
	case "install":
		return installCapabilityDeps(store, args[1:], out)
	case "list":
		if len(args) != 1 {
			return errors.New("usage: cohort capability deps list")
		}
		return listCapabilityDeps(store, out)
	default:
		return fmt.Errorf("unknown capability deps command %q", args[0])
	}
}

func planCapabilityDeps(store capability.Store, proposalID string, out io.Writer) error {
	plan, err := store.PlanDependencies(proposalID)
	if err != nil {
		return err
	}
	printDependencyPlan(out, plan)
	fmt.Fprintf(out, "approve: cohort capability deps approve %s\n", plan.ID)
	return nil
}

func approveCapabilityDeps(store capability.Store, planID string, out io.Writer) error {
	plan, err := store.ApproveDependencyPlan(planID)
	if err != nil {
		return err
	}
	printDependencyPlan(out, plan)
	fmt.Fprintf(out, "install: cohort capability deps install %s\n", plan.ID)
	return nil
}

func installCapabilityDeps(store capability.Store, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: cohort capability deps install <plan_id> [--dry-run]")
	}
	planID := ""
	dryRun := false
	for _, arg := range args {
		switch arg {
		case "--dry-run":
			dryRun = true
		default:
			if planID != "" {
				return errors.New("usage: cohort capability deps install <plan_id> [--dry-run]")
			}
			planID = arg
		}
	}
	if planID == "" {
		return errors.New("usage: cohort capability deps install <plan_id> [--dry-run]")
	}
	plan, records, err := store.InstallDependencyPlan(planID, capability.DependencyInstallOptions{DryRun: dryRun})
	if err != nil {
		return err
	}
	printDependencyPlan(out, plan)
	if len(records) > 0 {
		fmt.Fprintln(out, "")
		printDependencyInstalls(out, records)
	}
	return nil
}

func listCapabilityDeps(store capability.Store, out io.Writer) error {
	state, err := store.LoadDependencies()
	if err != nil {
		return err
	}
	if len(state.Plans) == 0 {
		fmt.Fprintln(out, "no dependency plans recorded")
		return nil
	}
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "PLAN\tSTATUS\tCAPABILITY\tACTIONS\tRISK")
	for _, plan := range state.Plans {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\n", plan.ID, plan.Status, plan.CapabilityID, len(plan.Actions), plan.Risk)
	}
	return tw.Flush()
}

func printDependencyPlan(out io.Writer, plan capability.DependencyPlan) {
	fmt.Fprintf(out, "plan: %s\n", plan.ID)
	fmt.Fprintf(out, "status: %s\n", plan.Status)
	fmt.Fprintf(out, "proposal: %s\n", plan.ProposalID)
	fmt.Fprintf(out, "capability: %s\n", plan.CapabilityID)
	fmt.Fprintf(out, "risk: %s\n", plan.Risk)
	if len(plan.Actions) == 0 {
		return
	}
	fmt.Fprintln(out, "")
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ACTION\tMANAGER\tSCOPE\tNAME\tCOMMAND")
	for _, action := range plan.Actions {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", action.ID, action.Manager, action.Scope, action.Name, strings.Join(action.Command, " "))
	}
	_ = tw.Flush()
}

func printDependencyInstalls(out io.Writer, records []capability.DependencyInstall) {
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "INSTALL\tSTATUS\tMANAGER\tNAME\tEXIT")
	for _, record := range records {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\n", record.ID, record.Status, record.Manager, record.Name, record.ExitCode)
	}
	_ = tw.Flush()
}
