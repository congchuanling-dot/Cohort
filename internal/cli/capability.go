package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"cohort/internal/capability"
)

func runCapabilityCommand(args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: cohort capability list|gaps|suggestions|show|propose|build|verify|promote|disable ...")
	}
	projectRoot, err := os.Getwd()
	if err != nil {
		return err
	}
	store := capability.NewStore(projectRoot)
	switch args[0] {
	case "list":
		return printCapabilityList(store, out)
	case "gaps":
		return printCapabilityGaps(store, out)
	case "suggestions":
		return printCapabilitySuggestions(store, out)
	case "show":
		if len(args) != 2 {
			return errors.New("usage: cohort capability show <id>")
		}
		return printCapabilityItem(store, args[1], out)
	case "propose":
		if len(args) < 2 {
			return errors.New(`usage: cohort capability propose "task or capability gap"`)
		}
		task := strings.Join(args[1:], " ")
		return proposeCapability(store, task, out)
	case "build":
		if len(args) != 2 {
			return errors.New("usage: cohort capability build <proposal_id>")
		}
		return buildCapability(store, args[1], out)
	case "verify":
		if len(args) != 2 {
			return errors.New("usage: cohort capability verify <capability_id>")
		}
		return verifyCapability(store, args[1], out)
	case "promote":
		if len(args) != 2 {
			return errors.New("usage: cohort capability promote <capability_id>")
		}
		return promoteCapability(store, args[1], out)
	case "disable":
		if len(args) != 2 {
			return errors.New("usage: cohort capability disable <capability_id>")
		}
		return disableCapability(store, args[1], out)
	default:
		return fmt.Errorf("unknown capability command %q", args[0])
	}
}

func printCapabilityList(store capability.Store, out io.Writer) error {
	registry, err := store.Load()
	if err != nil {
		return err
	}
	if len(registry.Capabilities) == 0 {
		fmt.Fprintln(out, "no capabilities registered")
		return nil
	}
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tSTATUS\tTYPE\tRISK\tENTRY")
	for _, item := range registry.Capabilities {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", item.ID, item.Status, item.Type, item.Risk, item.Entry)
	}
	return tw.Flush()
}

func printCapabilityGaps(store capability.Store, out io.Writer) error {
	registry, err := store.Load()
	if err != nil {
		return err
	}
	if len(registry.Gaps) == 0 {
		fmt.Fprintln(out, "no capability gaps recorded")
		return nil
	}
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tSTATUS\tMISSING_CAPABILITY\tSOURCE\tTASK")
	for _, item := range registry.Gaps {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", item.ID, item.Status, item.MissingCapability, item.Source, item.Task)
	}
	return tw.Flush()
}

func printCapabilitySuggestions(store capability.Store, out io.Writer) error {
	suggestions, err := store.Suggestions()
	if err != nil {
		return err
	}
	if len(suggestions) == 0 {
		fmt.Fprintln(out, "no repeated capability gaps need suggestions")
		return nil
	}
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "MISSING_CAPABILITY\tCOUNT\tSOURCES\tEXAMPLE_TASK\tNEXT")
	for _, item := range suggestions {
		exampleTask := ""
		if len(item.ExampleTasks) > 0 {
			exampleTask = item.ExampleTasks[0]
		}
		fmt.Fprintf(tw, "%s\t%d\t%s\t%s\t%s\n", item.MissingCapability, item.Count, strings.Join(item.Sources, ","), exampleTask, item.NextCommand)
	}
	return tw.Flush()
}

func printCapabilityItem(store capability.Store, id string, out io.Writer) error {
	kind, item, err := store.Find(id)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "kind: %s\n", kind)
	data, err := json.MarshalIndent(item, "", "  ")
	if err != nil {
		return err
	}
	fmt.Fprintln(out, string(data))
	return nil
}

func proposeCapability(store capability.Store, task string, out io.Writer) error {
	gap, err := store.AddGap(capability.NewGapFromTask(task))
	if err != nil {
		return err
	}
	proposal, err := store.AddProposal(capability.NewProposalFromGap(gap))
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "gap: %s\n", gap.ID)
	fmt.Fprintf(out, "proposal: %s\n", proposal.ID)
	fmt.Fprintf(out, "missing_capability: %s\n", gap.MissingCapability)
	fmt.Fprintf(out, "registry: %s\n", store.RegistryPath())
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Next:")
	fmt.Fprintf(out, "  1. Review: cohort capability show %s\n", proposal.ID)
	fmt.Fprintf(out, "  2. Build scaffold: cohort capability build %s\n", proposal.ID)
	fmt.Fprintln(out, "  3. Verify and promote only after reviewing the generated Skill")
	return nil
}

func buildCapability(store capability.Store, proposalID string, out io.Writer) error {
	item, err := store.Build(proposalID)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "capability: %s\n", item.ID)
	fmt.Fprintf(out, "status: %s\n", item.Status)
	fmt.Fprintf(out, "type: %s\n", item.Type)
	fmt.Fprintf(out, "entry: %s\n", item.Entry)
	fmt.Fprintf(out, "verify: cohort capability verify %s\n", item.ID)
	fmt.Fprintf(out, "registry: %s\n", store.RegistryPath())
	return nil
}

func verifyCapability(store capability.Store, capabilityID string, out io.Writer) error {
	item, output, err := store.Verify(capabilityID)
	if output != "" {
		fmt.Fprintln(out, output)
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "capability: %s\n", item.ID)
	fmt.Fprintf(out, "status: %s\n", item.Status)
	fmt.Fprintf(out, "verified_at: %s\n", item.Verification.LastPassedAt.Format("2006-01-02T15:04:05Z07:00"))
	fmt.Fprintf(out, "promote: cohort capability promote %s\n", item.ID)
	return nil
}

func promoteCapability(store capability.Store, capabilityID string, out io.Writer) error {
	item, err := store.Promote(capabilityID)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "capability: %s\n", item.ID)
	fmt.Fprintf(out, "status: %s\n", item.Status)
	fmt.Fprintf(out, "entry: %s\n", item.Entry)
	return nil
}

func disableCapability(store capability.Store, capabilityID string, out io.Writer) error {
	item, err := store.Disable(capabilityID)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "capability: %s\n", item.ID)
	fmt.Fprintf(out, "status: %s\n", item.Status)
	return nil
}
