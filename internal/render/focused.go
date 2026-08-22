package render

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/maheshrijal/mysq/internal/model"
)

func Focused(w io.Writer, section string, ctx *model.Context) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	switch section {
	case "queries":
		fmt.Fprintln(tw, "TOTAL\tSHARE\tCALLS\tMEAN\tEXAMINED\tSENT\tTMP DISK\tACTIVE USER\tSTATEMENT")
		var total float64
		for _, item := range ctx.Queries {
			total += item.TotalLatencyMillis
		}
		for _, item := range ctx.Queries {
			share := 0.0
			if total > 0 {
				share = item.TotalLatencyMillis * 100 / total
			}
			users := "—"
			if len(item.ActiveUsers) > 0 {
				users = strings.Join(item.ActiveUsers, ",")
			}
			fmt.Fprintf(tw, "%s\t%.1f%%\t%s\t%.2fms\t%s\t%s\t%s\t%s\t%s\n", duration(item.TotalLatencyMillis), share,
				humanCount(item.Calls), item.MeanLatencyMillis, humanCount(item.RowsExamined), humanCount(item.RowsSent),
				humanCount(item.TmpDiskTables), users, truncate(item.Statement, 76))
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
		fmt.Fprintln(tw, "ID\tUSER\tHOST\tTIME\tCOMMAND\tWAIT\tDIGEST\tSTATEMENT")
		for _, item := range ctx.Processes {
			fmt.Fprintf(tw, "%d\t%s\t%s\t%ds\t%s\t%s\t%s\t%s\n", item.ID, item.User, item.Host, item.Seconds,
				item.Command, item.WaitEvent, item.Digest, truncate(item.Statement, 76))
		}
	case "transactions":
		fmt.Fprintln(tw, "TRX\tPROCESS\tUSER\tHOST\tAGE\tSTATE\tROWS LOCKED\tROWS MODIFIED\tTABLES LOCKED\tSTATEMENT")
		for _, item := range ctx.Transactions {
			fmt.Fprintf(tw, "%s\t%d\t%s\t%s\t%ds\t%s\t%d\t%d\t%d\t%s\n", item.ID, item.ProcessID,
				item.User, item.Host, item.AgeSeconds, item.State, item.RowsLocked, item.RowsModified,
				item.TablesLocked, truncate(item.Statement, 76))
		}
	case "locks":
		fmt.Fprintln(tw, "WAITING TRX\tBLOCKING TRX\tOBJECT\tINDEX\tTYPE\tMODE")
		for _, item := range ctx.Locks {
			fmt.Fprintf(tw, "%s\t%s\t%s.%s\t%s\t%s\t%s\n", item.WaitingTransaction, item.BlockingTransaction,
				item.Schema, item.Table, item.Index, item.LockType, item.LockMode)
		}
	case "metadata-locks":
		fmt.Fprintln(tw, "STATUS\tPROCESS\tUSER\tHOST\tOBJECT TYPE\tOBJECT\tLOCK TYPE\tDURATION")
		for _, item := range ctx.MetadataLocks {
			fmt.Fprintf(tw, "%s\t%d\t%s\t%s\t%s\t%s.%s\t%s\t%s\n", item.Status, item.ProcessID,
				item.User, item.Host, item.ObjectType, item.Schema, item.Object, item.LockType, item.Duration)
		}
	case "waits":
		fmt.Fprintln(tw, "COUNT\tTOTAL\tMEAN\tMAX\tCLASS\tEVENT")
		for _, item := range ctx.WaitEvents {
			fmt.Fprintf(tw, "%s\t%s\t%.1fµs\t%s\t%s\t%s\n", humanCount(item.Count), duration(item.TotalLatencyMillis),
				item.MeanLatencyMicros, duration(item.MaxLatencyMillis), item.Class, item.Name)
		}
	case "memory":
		fmt.Fprintln(tw, "CURRENT\tHIGH WATER\tALLOCATIONS\tCONSUMER")
		for _, item := range ctx.MemoryConsumers {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", humanBytes(item.CurrentBytes), humanBytes(item.HighBytes),
				humanCount(item.Allocations), item.Name)
		}
	case "engine":
		m := ctx.Metrics
		fmt.Fprintln(tw, "SIGNAL\tVALUE")
		fmt.Fprintf(tw, "data_reads_per_second\t%.2f\ndata_writes_per_second\t%.2f\ndata_fsyncs_per_second\t%.2f\n", m.DataReadsPerSecond, m.DataWritesPerSecond, m.DataFsyncsPerSecond)
		fmt.Fprintf(tw, "pending_reads\t%d\npending_writes\t%d\npending_fsyncs\t%d\n", m.PendingReads, m.PendingWrites, m.PendingFsyncs)
		fmt.Fprintf(tw, "redo_bytes_per_second\t%.2f\nredo_writes_per_second\t%.2f\nredo_fsyncs_per_second\t%.2f\n", m.RedoBytesPerSecond, m.RedoWritesPerSecond, m.RedoFsyncsPerSecond)
		fmt.Fprintf(tw, "redo_checkpoint_age\t%s\nredo_capacity\t%s\nredo_checkpoint_age_percent\t%.2f%%\n", humanBytes(m.RedoCheckpointAgeBytes), humanBytes(m.RedoCapacityBytes), m.RedoCheckpointAgePct)
		fmt.Fprintf(tw, "buffer_pool_data\t%s\nbuffer_pool_dirty\t%s\nbuffer_pool_waits_per_second\t%.2f\n", humanBytes(m.BufferPoolDataBytes), humanBytes(m.BufferPoolDirtyBytes), m.BufferPoolWaitsPerSec)
		fmt.Fprintf(tw, "network_in_per_second\t%s/s\nnetwork_out_per_second\t%s/s\nfull_scans_per_second\t%.2f\nsort_merge_passes_per_second\t%.2f\n", humanBytes(uint64(m.NetworkInBytesPerSec)), humanBytes(uint64(m.NetworkOutBytesPerSec)), m.FullScansPerSecond, m.SortMergePassesPerSec)
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
