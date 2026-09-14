package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func newRootCommand() *cobra.Command {
	rootCommand := &cobra.Command{
		Use:   "strata",
		Short: "Layered, multi-format configuration library for Go with XDG cascading, provenance tracking, and CLI bridges",
		CompletionOptions: cobra.CompletionOptions{
			HiddenDefaultCmd: true,
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			showVersion, err := cmd.Flags().GetBool("version")
			if err != nil {
				return fmt.Errorf("read version flag: %w", err)
			}

			if showVersion {
				_, err := fmt.Fprintf(
					cmd.OutOrStdout(),
					"strata %s (commit: %s, built: %s)\n",
					version,
					commit,
					date,
				)
				if err != nil {
					return fmt.Errorf("print version: %w", err)
				}

				return nil
			}

			if err := cmd.Help(); err != nil {
				return fmt.Errorf("show help: %w", err)
			}

			return nil
		},
	}
	rootCommand.Flags().BoolP("version", "v", false, "Print version")

	return rootCommand
}

func Execute() {
	err := newRootCommand().Execute()
	if err != nil {
		os.Exit(1)
	}
}
