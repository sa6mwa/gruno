package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"pkt.systems/pslog"
)

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "gru",
		Short:         "Run Bruno .bru collections",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			structured, _ := cmd.Flags().GetBool("structured")
			levelStr, _ := cmd.Flags().GetString("log-level")
			caller, _ := cmd.Flags().GetBool("log-caller")
			levelFlagSet := cmd.Flags().Lookup("log-level") != nil && cmd.Flags().Lookup("log-level").Changed
			logger, err := newLogger(structured, levelStr, levelFlagSet, caller, os.Stdout)
			if err != nil {
				return err
			}
			ctx := pslog.ContextWithLogger(cmd.Context(), logger)
			cmd.SetContext(ctx)
			return nil
		},
	}

	addLoggingFlags(root.PersistentFlags())
	root.AddCommand(newRunCmd())
	root.AddCommand(newImportCmd())
	return root
}

func main() {
	if err := newRootCmd().Execute(); err != nil {
		// Cobra parse / usage errors
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func loggerFromCmd(cmd *cobra.Command) pslog.Logger {
	if cmd == nil {
		return pslog.NewWithOptions(os.Stdout, pslog.Options{MinLevel: pslog.InfoLevel})
	}
	if logger := pslog.LoggerFromContext(cmd.Context()); logger != nil {
		return logger
	}
	// Fallback: build from flags if context missing (tests)
	structured, _ := cmd.Flags().GetBool("structured")
	levelStr, _ := cmd.Flags().GetString("log-level")
	caller, _ := cmd.Flags().GetBool("log-caller")
	levelFlagSet := cmd.Flags().Lookup("log-level") != nil && cmd.Flags().Lookup("log-level").Changed
	logger, err := newLogger(structured, levelStr, levelFlagSet, caller, os.Stdout)
	if err != nil {
		return pslog.NewWithOptions(os.Stdout, pslog.Options{MinLevel: pslog.InfoLevel})
	}
	return logger
}

func addLoggingFlags(flags *pflag.FlagSet) {
	if flags.Lookup("log-level") == nil {
		flags.String("log-level", "info", "Log level (trace|debug|info|warn|error)")
	}
	if flags.Lookup("structured") == nil {
		flags.Bool("structured", false, "Emit structured JSON logs")
	}
	if flags.Lookup("log-caller") == nil {
		flags.Bool("log-caller", false, "Include caller function name on each log line")
	}
}
