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
		return errors.New("usage: cohort capability list|gaps|show|propose ...")
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
	fmt.Fprintln(out, "  2. Implement a Skill/Tool scaffold manually or in a future build step")
	fmt.Fprintln(out, "  3. Add smoke test evidence before promoting the capability")
	return nil
}
