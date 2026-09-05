package analyze

import (
	"fmt"
	"sort"
	"strings"

	"github.com/maheshrijal/mysq/internal/model"
)

// BlockingChains groups captured row-lock edges by root. It never infers a
// metadata-lock relationship from matching object names alone.
func BlockingChains(ctx *model.Context) []model.BlockingChain {
	children := map[string][]model.LockWait{}
	waiting := map[string]bool{}
	owners := map[string]model.Transaction{}
	for _, trx := range ctx.Transactions {
		owners[trx.ID] = trx
	}
	for _, edge := range ctx.Locks {
		children[edge.BlockingTransaction] = append(children[edge.BlockingTransaction], edge)
		waiting[edge.WaitingTransaction] = true
	}
	roots, others := []string{}, []string{}
	for id := range children {
		if !waiting[id] {
			roots = append(roots, id)
		} else {
			others = append(others, id)
		}
	}
	sort.Strings(roots)
	sort.Strings(others)
	result := []model.BlockingChain{}
	covered := map[string]bool{}
	for _, root := range append(roots, others...) {
		if waiting[root] && covered[root] {
			continue
		}
		chain := model.BlockingChain{RootTransaction: root, Complete: true, Transactions: []model.Transaction{}, Edges: []model.LockWait{}, Caveats: []string{"Point-in-time evidence; transaction age is not lock-wait duration. Metadata-lock owners are shown separately as candidates."}}
		visited := map[string]bool{}
		queue := []string{root}
		for len(queue) > 0 {
			id := queue[0]
			queue = queue[1:]
			if visited[id] {
				continue
			}
			visited[id] = true
			covered[id] = true
			if trx, ok := owners[id]; ok {
				chain.Transactions = append(chain.Transactions, trx)
			} else {
				chain.Complete = false
				chain.Caveats = append(chain.Caveats, fmt.Sprintf("Transaction %s owner not captured; it may have ended or exceeded the collection limit.", id))
			}
			edges := append([]model.LockWait(nil), children[id]...)
			sort.Slice(edges, func(i, j int) bool {
				a, b := edges[i], edges[j]
				return strings.Join([]string{a.WaitingTransaction, a.Schema, a.Table, a.Index, a.LockType, a.LockMode}, "\x00") < strings.Join([]string{b.WaitingTransaction, b.Schema, b.Table, b.Index, b.LockType, b.LockMode}, "\x00")
			})
			chain.Edges = append(chain.Edges, edges...)
			for _, edge := range edges {
				queue = append(queue, edge.WaitingTransaction)
			}
		}
		chain.WaiterCount = len(visited) - 1
		// Kahn's algorithm catches cycles even when reachable from an ordinary root.
		degree := map[string]int{}
		for id := range visited {
			degree[id] = 0
		}
		for _, edge := range chain.Edges {
			degree[edge.WaitingTransaction]++
		}
		queue = nil
		for id, d := range degree {
			if d == 0 {
				queue = append(queue, id)
			}
		}
		removed := 0
		for len(queue) > 0 {
			id := queue[0]
			queue = queue[1:]
			removed++
			for _, edge := range children[id] {
				degree[edge.WaitingTransaction]--
				if degree[edge.WaitingTransaction] == 0 {
					queue = append(queue, edge.WaitingTransaction)
				}
			}
		}
		if removed < len(visited) {
			if waiting[root] {
				chain.WaiterCount++
			}
			chain.Complete = false
			chain.Caveats = append(chain.Caveats, "Captured graph contains a cycle; this root is an entry point, not a proven ultimate blocker.")
		}
		for _, capability := range ctx.Capabilities {
			if !capability.Available && (capability.Name == "row lock waits" || capability.Name == "active transactions") {
				chain.Complete = false
				chain.Caveats = append(chain.Caveats, capability.Name+": "+capability.Reason)
			}
		}
		result = append(result, chain)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].WaiterCount != result[j].WaiterCount {
			return result[i].WaiterCount > result[j].WaiterCount
		}
		return result[i].RootTransaction < result[j].RootTransaction
	})
	return result
}
