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
	case "blockers":
		fmt.Fprintln(tw, "BLOCKING CHAINS · captured blocker → waiter edges")
		if len(ctx.BlockingChains) == 0 {
			fmt.Fprintln(tw, "No row-lock chain captured; inspect coverage and metadata locks below.")
		}
		for _, chain := range ctx.BlockingChains {
			fmt.Fprintf(tw, "\nRoot transaction %s · %d distinct waiters · complete=%t\n", chain.RootTransaction, chain.WaiterCount, chain.Complete)
			for _, trx := range chain.Transactions {
				fmt.Fprintf(tw, "  Transaction %s · connection %d · %s@%s · age %ds · %s\n    %s\n", trx.ID, trx.ProcessID, trx.User, trx.Host, trx.AgeSeconds, trx.State, trx.Statement)
			}
			for _, edge := range chain.Edges {
				fmt.Fprintf(tw, "  %s → %s · %s.%s · %s\n", edge.BlockingTransaction, edge.WaitingTransaction, edge.Schema, edge.Table, edge.LockMode)
			}
			for _, note := range chain.Caveats {
				fmt.Fprintln(tw, "  Note: "+note)
			}
		}
		fmt.Fprintln(tw, "\nMETADATA LOCKS · granted owners are candidates, not proven edges")
		for _, lock := range ctx.MetadataLocks {
			fmt.Fprintf(tw, "%s · %s.%s · connection %d · %s@%s · %s\n", lock.Status, lock.Schema, lock.Object, lock.ProcessID, lock.User, lock.Host, lock.LockType)
		}
		for _, capability := range ctx.Capabilities {
			if !capability.Available {
				fmt.Fprintf(tw, "Unavailable: %s · %s\n", capability.Name, capability.Reason)
			}
		}

	case "queries":
		fmt.Fprintln(tw, "TOTAL\tSHARE\tCALLS\tMEAN\tP95\tP99\tMAX\tERRORS/WARN\tEXAMINED\tSENT\tACTIVE USER\tSTATEMENT")
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
			fmt.Fprintf(tw, "%s\t%.1f%%\t%s\t%.2fms\t%s\t%s\t%s\t%d/%d\t%s\t%s\t%s\t%s\n", duration(item.TotalLatencyMillis), share,
				humanCount(item.Calls), item.MeanLatencyMillis, duration(item.P95LatencyMillis), duration(item.P99LatencyMillis),
				duration(item.MaxLatencyMillis), item.Errors, item.Warnings, humanCount(item.RowsExamined), humanCount(item.RowsSent),
				users, truncate(item.Statement, 76))
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
		fmt.Fprintln(tw, "SAMPLE SHARE\tWAIT/S\tEVENTS/S\tSAMPLE COUNT\tCUM TOTAL\tCLASS\tEVENT")
		for _, item := range ctx.WaitEvents {
			fmt.Fprintf(tw, "%.1f%%\t%s/s\t%.2f\t%s\t%s\t%s\t%s\n", item.SampleSharePercent, duration(item.WaitMillisPerSecond),
				item.EventsPerSecond, humanCount(item.SampleCount), duration(item.TotalLatencyMillis), item.Class, item.Name)
		}
	case "io":
		fmt.Fprintln(tw, "READ/S\tWRITE/S\tREAD BYTES/S\tWRITE BYTES/S\tREAD LAT\tWRITE LAT\tWAIT/S\tINSTRUMENT")
		for _, item := range ctx.FileIO {
			fmt.Fprintf(tw, "%.2f\t%.2f\t%s/s\t%s/s\t%s\t%s\t%s/s\t%s\n", item.ReadsPerSecond, item.WritesPerSecond,
				humanBytes(uint64(item.ReadBytesPerSecond)), humanBytes(uint64(item.WriteBytesPerSecond)),
				duration(item.MeanReadLatencyMillis), duration(item.MeanWriteLatencyMillis), duration(item.WaitMillisPerSecond), item.Name)
		}
	case "errors":
		fmt.Fprintln(tw, "ERROR\tSQLSTATE\tSAMPLE/S\tSAMPLE\tTOTAL\tHANDLED\tLAST SEEN\tNAME")
		for _, item := range ctx.ServerErrors {
			fmt.Fprintf(tw, "%d\t%s\t%.2f\t%d\t%d\t%d\t%s\t%s\n", item.Number, item.SQLState,
				item.RaisedPerSecond, item.SampleRaised, item.Raised, item.Handled, item.LastSeen, item.Name)
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
		fmt.Fprintf(tw, "statement_errors_per_second\t%.2f\nstatement_warnings_per_second\t%.2f\ndeadlocks_per_second\t%.2f\nlock_timeouts_per_second\t%.2f\nthreads_created_per_second\t%.2f\n", m.StatementErrorsPerSec, m.StatementWarningsPerSec, m.DeadlocksPerSecond, m.LockTimeoutsPerSecond, m.ThreadsCreatedPerSecond)
	case "coverage":
		coverage := ctx.Instrumentation
		fmt.Fprintln(tw, "SIGNAL\tVALUE")
		fmt.Fprintf(tw, "digest_rows\t%d\ndigest_capacity\t%d\ndigest_utilization\t%.2f%%\ntotal_lost\t%d\ndisabled_consumers\t%s\n",
			coverage.DigestRows, coverage.DigestCapacity, coverage.DigestUtilizationPercent, coverage.TotalLost,
			strings.Join(coverage.DisabledConsumers, ","))
		keys := make([]string, 0, len(coverage.Lost))
		for key := range coverage.Lost {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			fmt.Fprintf(tw, "%s\t%d\n", key, coverage.Lost[key])
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
		fmt.Fprintf(tw, "applier_state\t%s\ntransaction_retries\t%d\nworkers\t%d\n", r.ApplierState, r.TransactionRetries, len(r.Workers))
		for _, worker := range r.Workers {
			fmt.Fprintf(tw, "worker_%d\t%s thread=%d retries=%d error=%d %s\n", worker.WorkerID, worker.ServiceState,
				worker.ThreadID, worker.ApplyingTransactionRetries, worker.LastErrorNumber, worker.LastErrorMessage)
		}
	default:
		return fmt.Errorf("unknown focused section %q", section)
	}
	return tw.Flush()
}
