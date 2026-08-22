package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"github.com/maheshrijal/mysqldot/internal/analyze"
	"github.com/maheshrijal/mysqldot/internal/collect"
	"github.com/maheshrijal/mysqldot/internal/compare"
	bundle "github.com/maheshrijal/mysqldot/internal/export"
	"github.com/maheshrijal/mysqldot/internal/history"
	"github.com/maheshrijal/mysqldot/internal/model"
	"github.com/maheshrijal/mysqldot/internal/render"
	terminalui "github.com/maheshrijal/mysqldot/internal/tui"
)

type App struct {
	Version string
	Out     io.Writer
	Err     io.Writer
	color   bool
}

type ExitError struct {
	Code int
	Err  error
}

func (e ExitError) Error() string { return e.Err.Error() }
func (e ExitError) Unwrap() error { return e.Err }

func New(version string) *cobra.Command {
	return newRoot(version, os.Stdout, os.Stderr)
}

func newRoot(version string, out, errOut io.Writer) *cobra.Command {
	app := &App{Version: version, Out: out, Err: errOut}
	root := &cobra.Command{
		Use:           "mysqldot",
		Short:         "MySQL diagnostics for humans and agents",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version,
		Long: `mysqldot is a read-only, findings-first MySQL diagnostic.

It samples MySQL's own status and Performance Schema, produces a polished terminal
report, remembers local snapshots, and exports a secret-free evidence bundle for
agents. It never creates objects or writes data in the inspected database.`,
		PersistentPreRun: func(cmd *cobra.Command, _ []string) {
			noColor, _ := cmd.Flags().GetBool("no-color")
			app.color = !noColor && os.Getenv("NO_COLOR") == "" && term.IsTerminal(os.Stdout.Fd())
		},
	}
	root.SetOut(out)
	root.SetErr(errOut)
	root.PersistentFlags().Bool("no-color", false, "disable ANSI color")
	root.AddCommand(app.inspectCommand(), app.tuiCommand(), app.exportCommand(), app.diffCommand(), app.snapshotsCommand(), app.initCommand())
	for _, section := range []string{"queries", "tables", "indexes", "processes", "transactions", "locks", "metadata-locks", "waits", "memory", "engine", "variables", "replication"} {
		root.AddCommand(app.focusedCommand(section))
	}
	return root
}

func (a *App) tuiCommand() *cobra.Command {
	var interval time.Duration
	command := &cobra.Command{
		Use:   "tui [mysql-dsn]",
		Short: "Open the live interactive terminal dashboard",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			argument := ""
			if len(args) == 1 {
				argument = args[0]
			}
			target, err := collect.ResolveConnection(argument)
			if err != nil {
				return err
			}
			inspect := func(ctx context.Context) (*model.Context, error) {
				collector := collect.New(a.Version)
				collector.Interval = interval
				result, err := collector.Inspect(ctx, target)
				if err != nil {
					return nil, err
				}
				analyze.Apply(result)
				store, storeErr := history.Open("")
				if storeErr == nil {
					_, storeErr = store.Save(result)
				}
				if storeErr != nil {
					result.Warnings = append(result.Warnings, "snapshot history: "+storeErr.Error())
				}
				return result, nil
			}
			export := func(ctx *model.Context) (string, error) {
				output := fmt.Sprintf("mysqldot-export-%s", time.Now().Format("20060102-150405.000"))
				result, err := bundle.Write(ctx, output, bundle.Options{})
				return result.Directory, err
			}
			return terminalui.Run(cmd.Context(), inspect, export)
		},
	}
	command.Flags().DurationVar(&interval, "interval", time.Second, "counter sampling interval")
	return command
}

type inspectFlags struct {
	format    string
	full      bool
	interval  time.Duration
	noStore   bool
	storePath string
	exportDir string
	failOn    string
}

func (a *App) inspectCommand() *cobra.Command {
	flags := inspectFlags{}
	command := &cobra.Command{
		Use:   "inspect [mysql-dsn]",
		Short: "Run the findings-first health inspection",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			argument := ""
			if len(args) == 1 {
				argument = args[0]
			}
			ctx, err := a.inspect(cmd.Context(), argument, flags.interval)
			if err != nil {
				return ExitError{Code: 3, Err: err}
			}
			if !flags.noStore {
				store, err := history.Open(flags.storePath)
				if err != nil {
					return fmt.Errorf("open snapshot history: %w", err)
				}
				if _, err := store.Save(ctx); err != nil {
					return fmt.Errorf("save snapshot history: %w", err)
				}
			}
			if flags.exportDir != "" {
				if _, err := bundle.Write(ctx, flags.exportDir, bundle.Options{}); err != nil {
					return fmt.Errorf("export agent bundle: %w", err)
				}
			}
			if err := a.render(cmd, ctx, flags.format, flags.full); err != nil {
				return err
			}
			if code := failCode(ctx, flags.failOn); code != 0 {
				return ExitError{Code: code, Err: fmt.Errorf("health gate failed at %s", flags.failOn)}
			}
			return nil
		},
	}
	f := command.Flags()
	f.StringVar(&flags.format, "format", "text", "output format: text, json, or markdown")
	f.BoolVar(&flags.full, "full", false, "include subsystem board and detailed tables")
	f.DurationVar(&flags.interval, "interval", time.Second, "counter sampling interval")
	f.BoolVar(&flags.noStore, "no-store", false, "do not save the snapshot in local history")
	f.StringVar(&flags.storePath, "store", "", "snapshot history directory (default: XDG state directory)")
	f.StringVar(&flags.exportDir, "export-dir", "", "also write the complete agent bundle to this new directory")
	f.StringVar(&flags.failOn, "fail-on", "none", "health gate: critical, warning, note, or none")
	return command
}

func (a *App) exportCommand() *cobra.Command {
	var output string
	var interval time.Duration
	var zipOutput bool
	command := &cobra.Command{
		Use:   "export [mysql-dsn]",
		Short: "Collect and write a native agent evidence bundle",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			argument := ""
			if len(args) == 1 {
				argument = args[0]
			}
			ctx, err := a.inspect(cmd.Context(), argument, interval)
			if err != nil {
				return ExitError{Code: 3, Err: err}
			}
			result, err := bundle.Write(ctx, output, bundle.Options{Zip: zipOutput})
			if err != nil {
				return err
			}
			fmt.Fprintf(a.Out, "Exported %d secret-free artifacts to %s\n", len(result.Files), result.Directory)
			if result.Archive != "" {
				fmt.Fprintf(a.Out, "Archive: %s\n", result.Archive)
			}
			return nil
		},
	}
	command.Flags().StringVarP(&output, "out", "o", "", "new output directory (default: timestamped directory in cwd)")
	command.Flags().DurationVar(&interval, "interval", time.Second, "counter sampling interval")
	command.Flags().BoolVar(&zipOutput, "zip", false, "also create a ZIP archive")
	return command
}

func (a *App) focusedCommand(section string) *cobra.Command {
	var jsonOutput bool
	var interval time.Duration
	command := &cobra.Command{
		Use:   section + " [mysql-dsn]",
		Short: focusedDescription(section),
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			argument := ""
			if len(args) == 1 {
				argument = args[0]
			}
			ctx, err := a.inspect(cmd.Context(), argument, interval)
			if err != nil {
				return ExitError{Code: 3, Err: err}
			}
			value := focusedValue(section, ctx)
			if jsonOutput {
				return writeJSON(a.Out, value)
			}
			return render.Focused(a.Out, section, ctx)
		},
	}
	command.Flags().BoolVar(&jsonOutput, "json", false, "emit JSON")
	command.Flags().DurationVar(&interval, "interval", time.Second, "counter sampling interval")
	return command
}

func (a *App) diffCommand() *cobra.Command {
	var since time.Duration
	var fingerprint, storePath string
	var jsonOutput bool
	command := &cobra.Command{
		Use:   "diff",
		Short: "Compare two local snapshots offline",
		RunE: func(cmd *cobra.Command, _ []string) error {
			store, err := history.Open(storePath)
			if err != nil {
				return err
			}
			if fingerprint == "" {
				items, err := store.List()
				if err != nil {
					return err
				}
				if len(items) == 0 {
					return errors.New("no snapshots found; run mysqldot inspect first")
				}
				if len(items) > 1 {
					return errors.New("multiple databases are stored; pass --fingerprint from mysqldot snapshots list")
				}
				fingerprint = items[0].Fingerprint
			}
			baseline, current, err := store.Pair(fingerprint, since)
			if err != nil {
				return err
			}
			if current == nil || baseline == nil {
				return fmt.Errorf("not enough history for %s at least %s apart", fingerprint, since)
			}
			report := compare.Build(baseline, current)
			if jsonOutput {
				return writeJSON(a.Out, report)
			}
			_, err = io.WriteString(a.Out, compare.Text(report))
			return err
		},
	}
	command.Flags().DurationVar(&since, "since", time.Hour, "minimum age between the latest and baseline snapshot")
	command.Flags().StringVar(&fingerprint, "fingerprint", "", "database fingerprint (inferred when only one exists)")
	command.Flags().StringVar(&storePath, "store", "", "snapshot history directory")
	command.Flags().BoolVar(&jsonOutput, "json", false, "emit JSON")
	return command
}

func (a *App) snapshotsCommand() *cobra.Command {
	var storePath string
	command := &cobra.Command{Use: "snapshots", Short: "Inspect local snapshot history"}
	list := &cobra.Command{
		Use: "list", Short: "List databases with stored snapshots",
		RunE: func(cmd *cobra.Command, _ []string) error {
			store, err := history.Open(storePath)
			if err != nil {
				return err
			}
			items, err := store.List()
			if err != nil {
				return err
			}
			fmt.Fprintln(a.Out, "FINGERPRINT               SNAPSHOTS  NEWEST                    TARGET")
			for _, item := range items {
				fmt.Fprintf(a.Out, "%-25s %-10d %-25s %s/%s\n", item.Fingerprint, item.Count, item.Newest.Format(time.RFC3339), item.Host, item.Database)
			}
			return nil
		},
	}
	list.Flags().StringVar(&storePath, "store", "", "snapshot history directory")
	command.AddCommand(list)
	return command
}

func (a *App) initCommand() *cobra.Command {
	var user string
	command := &cobra.Command{
		Use:   "init",
		Short: "Print reviewed SQL for a least-privilege monitoring user; execute nothing",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !regexp.MustCompile(`^[A-Za-z0-9_]+$`).MatchString(user) {
				return errors.New("--user may contain only letters, numbers, and underscores")
			}
			fmt.Fprintf(a.Out, `-- mysqldot never executes this SQL. Review and replace the password first.
CREATE USER IF NOT EXISTS '%s'@'%%' IDENTIFIED BY 'REPLACE_WITH_A_LONG_RANDOM_PASSWORD';
GRANT PROCESS, REPLICATION CLIENT ON *.* TO '%s'@'%%';
GRANT SELECT ON performance_schema.* TO '%s'@'%%';

-- For table and index metadata in each application database, grant SELECT on
-- only those databases. MySQL couples metadata visibility to object privileges:
-- GRANT SELECT ON your_database.* TO '%s'@'%%';

SHOW GRANTS FOR '%s'@'%%';
`, user, user, user, user, user)
			return nil
		},
	}
	command.Flags().StringVar(&user, "user", "mysqldot_monitor", "monitoring username")
	return command
}

func (a *App) inspect(ctx context.Context, argument string, interval time.Duration) (*model.Context, error) {
	target, err := collect.ResolveConnection(argument)
	if err != nil {
		return nil, err
	}
	collector := collect.New(a.Version)
	collector.Interval = interval
	result, err := collector.Inspect(ctx, target)
	if err != nil {
		return nil, err
	}
	analyze.Apply(result)
	return result, nil
}

func (a *App) render(cmd *cobra.Command, ctx *model.Context, format string, full bool) error {
	switch strings.ToLower(format) {
	case "text":
		width, _, err := term.GetSize(os.Stdout.Fd())
		if err != nil || width < 60 {
			width = 100
		}
		return render.Text(a.Out, ctx, render.Options{Full: full, Color: a.color, Width: width})
	case "json":
		return writeJSON(a.Out, ctx)
	case "markdown", "md":
		return render.Markdown(a.Out, ctx)
	default:
		return fmt.Errorf("unsupported format %q (want text, json, or markdown)", format)
	}
}

func writeJSON(w io.Writer, value any) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func failCode(ctx *model.Context, threshold string) int {
	switch strings.ToLower(threshold) {
	case "none":
		return 0
	case "critical":
		if ctx.Health.Critical > 0 {
			return 2
		}
	case "warning", "warn":
		if ctx.Health.Critical > 0 {
			return 2
		}
		if ctx.Health.Warnings > 0 {
			return 1
		}
	case "note", "info":
		if ctx.Health.Critical > 0 {
			return 2
		}
		if ctx.Health.Warnings > 0 || ctx.Health.Notes > 0 {
			return 1
		}
	default:
		return 64
	}
	return 0
}

func focusedValue(section string, ctx *model.Context) any {
	switch section {
	case "queries":
		return ctx.Queries
	case "tables":
		return ctx.Tables
	case "indexes":
		return ctx.Indexes
	case "processes":
		return ctx.Processes
	case "transactions":
		return ctx.Transactions
	case "locks":
		return ctx.Locks
	case "metadata-locks":
		return ctx.MetadataLocks
	case "waits":
		return ctx.WaitEvents
	case "memory":
		return ctx.MemoryConsumers
	case "engine":
		return ctx.Metrics
	case "variables":
		return ctx.Variables
	case "replication":
		return ctx.Replication
	default:
		return nil
	}
}

func focusedDescription(section string) string {
	descriptions := map[string]string{
		"queries": "Show top normalized statements by total latency", "tables": "Show table size and I/O activity",
		"indexes": "Show index definitions and usage", "processes": "Show the redacted connection snapshot",
		"transactions": "Show active InnoDB transactions", "locks": "Show active InnoDB row lock waits",
		"metadata-locks": "Show active and pending metadata locks", "waits": "Show top Performance Schema wait events",
		"memory": "Show top MySQL memory consumers", "engine": "Show sampled InnoDB I/O, redo, and network metrics",
		"variables":   "Show sorted server configuration",
		"replication": "Show replica thread health and lag",
	}
	return descriptions[section]
}
