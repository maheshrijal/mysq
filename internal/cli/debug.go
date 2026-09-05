package cli

import (
	"errors"
	"fmt"

	"github.com/maheshrijal/mysq/internal/debuglog"
	"github.com/spf13/cobra"
)

// Wrap command execution so logs are closed on both success and RunE errors.
// Cobra's post-run hooks do not run when RunE fails.
func enableDebugLog(command *cobra.Command, path *string, version string) {
	if run := command.RunE; run != nil {
		command.RunE = func(cmd *cobra.Command, args []string) (err error) {
			if *path == "" {
				return run(cmd, args)
			}
			previous := cmd.Context()
			ctx, closeLog, err := debuglog.Open(previous, *path, version)
			if err != nil {
				return fmt.Errorf("open debug log: %w", err)
			}
			cmd.SetContext(ctx)
			done := debuglog.Start(ctx, cmd.CommandPath())
			defer func() {
				debuglog.Result(ctx, cmd.CommandPath(), err)
				done()
				cmd.SetContext(previous)
				if closeErr := closeLog(); closeErr != nil {
					err = errors.Join(err, fmt.Errorf("write debug log: %w", closeErr))
				}
			}()
			return run(cmd, args)
		}
	}
	for _, child := range command.Commands() {
		enableDebugLog(child, path, version)
	}
}
