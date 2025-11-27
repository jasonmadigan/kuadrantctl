package cmd

import (
	"fmt"
	"os/exec"

	"github.com/spf13/cobra"
)

func dnsCommand() *cobra.Command {
	versionCmd := &cobra.Command{
		Use:   "dns",
		Short: "DNS Operator command line utility",
		Long:  "DNS Operator command line utility",
		RunE:  runDNS,
	}

	// this will pass all flags as args
	versionCmd.DisableFlagParsing = true
	return versionCmd
}

func runDNS(_ *cobra.Command, args []string) error {

	out, err := exec.Command("kubectl-kuadrant_dns", args...).Output()
	if err != nil {
		// display help tooltip
		tooltip, tooltipErr := exec.Command("kubectl-kuadrant_dns", []string{args[0], "--help"}...).Output()
		if tooltipErr == nil {
			fmt.Printf("%s\n", tooltip)
		}

		return fmt.Errorf("failed to run dns plugin: %w", err)
	}
	fmt.Printf("%s\n", out)
	return nil
}
