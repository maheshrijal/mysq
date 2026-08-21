package render

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/maheshrijal/mysqldot/internal/model"
)

func Focused(w io.Writer, section string, ctx *model.Context) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	switch section {
	case "queries":
		fmt.Fprintln(tw, "TOTAL\tSHARE\tCALLS\tMEAN\tROWS EXAMINED\tSTATEMENT")
		var total float64
		for _, item := range ctx.Queries {
			total += item.TotalLatencyMillis
		}
		for _, item := range ctx.Queries {
			share := 0.0
			if total > 0 {
				share = item.TotalLatencyMillis * 100 / total
			}
			fmt.Fprintf(tw, "%s\t%.1f%%\t%s\t%.2fms\t%s\t%s\n", duration(item.TotalLatencyMillis), share,
				humanCount(item.Calls), item.MeanLatencyMillis, humanCount(item.RowsExamined), truncate(item.Statement, 76))
		}
	case "tables":
		fmt.Fprintln(tw, "SIZE\tROWS\tREADS\tWRITES\tPK\tTABLE")
		for _, item := range ctx.Tables {
			pk := "yes"
			if !item.HasPrimaryKey {
				pk = "NO"
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s.%s\n", humanBytes(item.TotalBytes), humanCount(item.EstimatedRows),
				humanCount(item.Reads), humanCount(item.Writes), pk, item.Schema, item.Name)
		}
	case "indexes":
		fmt.Fprintln(tw, "READS\tWRITES\tUNIQUE\tVISIBLE\tINDEX\tCOLUMNS")
		for _, item := range ctx.Indexes {
			fmt.Fprintf(tw, "%s\t%s\t%t\t%t\t%s.%s.%s\t%s\n", humanCount(item.Reads), humanCount(item.Writes),
				item.Unique, item.Visible, item.Schema, item.Table, item.Name, item.Columns)
		}
	case "processes":
		fmt.Fprintln(tw, "ID\tUSER\tHOST\tTIME\tCOMMAND\tSTATE\tSTATEMENT")
		for _, item := range ctx.Processes {
			fmt.Fprintf(tw, "%d\t%s\t%s\t%ds\t%s\t%s\t%s\n", item.ID, item.User, item.Host, item.Seconds,
				item.Command, item.State, truncate(item.Statement, 76))
		}
	case "locks":
		fmt.Fprintln(tw, "WAITING TRX\tBLOCKING TRX\tOBJECT\tINDEX\tTYPE\tMODE")
		for _, item := range ctx.Locks {
			fmt.Fprintf(tw, "%s\t%s\t%s.%s\t%s\t%s\t%s\n", item.WaitingTransaction, item.BlockingTransaction,
				item.Schema, item.Table, item.Index, item.LockType, item.LockMode)
		}
	case "variables":
		fmt.Fprintln(tw, "VARIABLE\tVALUE")
		keys := make([]string, 0, len(ctx.Variables))
		for key := range ctx.Variables {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			fmt.Fprintf(tw, "%s\t%s\n", key, strings.ReplaceAll(ctx.Variables[key], "\n", " "))
		}
	case "replication":
		if ctx.Replication == nil {
			fmt.Fprintln(tw, "This server is not configured as a replica.")
			break
		}
		r := ctx.Replication
		lag := "unknown"
		if r.SecondsBehind != nil {
			lag = fmt.Sprintf("%ds", *r.SecondsBehind)
		}
		fmt.Fprintln(tw, "FIELD\tVALUE")
		fmt.Fprintf(tw, "source\t%s:%d\nchannel\t%s\nio_running\t%s\nsql_running\t%s\nlag\t%s\nlast_io_error\t%s\nlast_sql_error\t%s\n",
			r.SourceHost, r.SourcePort, r.Channel, r.IORunning, r.SQLRunning, lag, r.LastIOError, r.LastSQLError)
	default:
		return fmt.Errorf("unknown focused section %q", section)
	}
	return tw.Flush()
}
