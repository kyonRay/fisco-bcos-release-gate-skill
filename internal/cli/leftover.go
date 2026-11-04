package cli

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/kyonRay/fisco-bcos-release-gate-skill/internal/clusters"
	"github.com/kyonRay/fisco-bcos-release-gate-skill/internal/execution"
	"github.com/kyonRay/fisco-bcos-release-gate-skill/internal/exitcode"
	"github.com/kyonRay/fisco-bcos-release-gate-skill/internal/workspace"
)

// stopDeadline bounds the engine's stop_all.sh, and the context is never the run's: cleanup has to
// finish after Ctrl-C cancelled the run.
const stopDeadline = 2 * time.Minute

// settle ends a run: it stops the run's nodes, closes the transcript, removes the cluster directory
// and releases the ports. After it returns nothing of the run is left but its record. Every command
// that builds a chain exits through here, on the normal path, on a gate failure and on Ctrl-C.
//
// With keep set (--keep-cluster) the chain and its directory stay and the entry loses its owner, so
// no later run reaps them; `fbt cluster down` is how they end.
//
// It returns the run's exit code and whether nodes are still running. Calling it twice is safe, so
// commands can defer it for their early returns.
func (rt *runtime) settle(keep bool, stderr io.Writer) (exitcode.Code, bool) {
	if rt.settled {
		return rt.Agg.Result(), rt.nodesLeft
	}
	rt.settled = true
	if !keep {
		rt.stopCluster(rt.Workspace.Cluster)
	}
	code := rt.Agg.Result()
	rt.Exec.Finish(code)
	rt.finish() // close the run transcript after the last event

	nodes := clusters.DiscoverNodes(rt.Workspace.Cluster, clusters.OSProber{})
	rt.nodesLeft = len(nodes) > 0
	switch {
	case keep:
		rt.keepEntry(nodes, stderr)
	case rt.nodesLeft:
		// Never delete a directory under a running node. The owner stays recorded, so the next fbt
		// run tries again once this one is gone.
		rt.recordNodes(nodes, rt.ownerOfRun())
		fmt.Fprintf(stderr, "cluster %s is still running after stop_all.sh (%d node processes under %s); its ports stay reserved.\n"+
			"  stop it with:  fbt cluster down --run-id %s\n", rt.Norm.RunID, len(nodes), rt.Workspace.Cluster, rt.Norm.RunID)
	default:
		if err := rt.Workspace.Archive(code != exitcode.OK); err != nil {
			fmt.Fprintf(stderr, "fbt: %v\n", err)
		}
		_ = rt.Registry.Release(rt.Norm.RunID)
	}
	return code, rt.nodesLeft
}

// keepEntry applies --keep-cluster. A chain that died during the run has nothing left to reserve
// ports for, but its directory is still what the operator asked to look at.
func (rt *runtime) keepEntry(nodes []clusters.Node, stderr io.Writer) {
	if len(nodes) == 0 {
		_ = rt.Registry.Release(rt.Norm.RunID)
		fmt.Fprintf(stderr, "no node is running; the cluster directory was kept at %s\n", rt.Workspace.Cluster)
		return
	}
	rt.recordNodes(nodes, nil)
	fmt.Fprintf(stderr, "cluster %s is still running (%d node processes under %s); its ports stay reserved.\n"+
		"  stop it with:  fbt cluster down --run-id %s\n", rt.Norm.RunID, len(nodes), rt.Workspace.Cluster, rt.Norm.RunID)
}

// recordNodes stores the processes and the owner on this run's entry, so liveness and reclaim have
// something to work on.
func (rt *runtime) recordNodes(nodes []clusters.Node, owner *clusters.Node) {
	if entry, err := rt.Registry.Resolve(rt.Norm.RunID, ""); err == nil {
		entry.Nodes = nodes
		entry.Owner = owner
		_ = rt.Registry.Update(entry)
	}
}

func (rt *runtime) ownerOfRun() *clusters.Node {
	if entry, err := rt.Registry.Resolve(rt.Norm.RunID, ""); err == nil {
		return entry.Owner
	}
	return nil
}

// stopCluster runs the engine's teardown when a node of this workspace is alive, then waits for the
// processes to leave the table. gate.sh and run_case.sh normally got there first; a failed bring-up,
// an interrupted run and every upgrade timeline did not. It reports whether all nodes are gone.
func (rt *runtime) stopCluster(ws string) bool {
	if len(clusters.DiscoverNodes(ws, clusters.OSProber{})) == 0 {
		return true
	}
	rt.Exec.Run(context.Background(), execution.Command{
		Script:   "cluster_down.sh",
		Args:     []string{"-o", ws},
		Requires: []string{"cluster"},
		Config:   rt.engineConfig(),
		Timeout:  stopDeadline,
	})
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		if len(clusters.DiscoverNodes(ws, clusters.OSProber{})) == 0 {
			return true
		}
	}
	return false
}

// runOf maps a cluster directory back to its run, when it is one fbt laid out. Only such a
// directory is ever deleted: `cluster down --workspace` accepts any path that looks like a cluster,
// and an operator's own directory is not fbt's to remove.
func (rt *runtime) runOf(cluster string) (workspace.Workspace, bool) {
	w, err := workspace.Layout(rt.Paths.State, filepath.Base(filepath.Dir(cluster)))
	if err != nil || w.Cluster != cluster {
		return workspace.Workspace{}, false
	}
	return w, true
}

// reapLeftovers cleans up after runs that could not clean up themselves. An entry whose fbt died has
// its chain stopped first; an entry whose nodes are all gone only needs its directory and record
// dealt with. Logs are kept in both cases, since nobody has looked at how that run ended.
//
// It uses a runtime of its own, so a teardown here cannot affect the verdict of the run about to
// start. Commands call it before their port check: an orphan's nodes are holding those ports.
func reapLeftovers(o GlobalOptions, stderr io.Writer) error {
	rt, err := newRuntime(o, "", io.Discard, stderr)
	if err != nil {
		return err
	}
	orphans, err := rt.Registry.Orphans()
	if err != nil {
		return err
	}
	for _, e := range orphans {
		if !rt.stopCluster(e.Workspace) {
			fmt.Fprintf(stderr, "cannot stop the leftover cluster of run %s under %s; stop it with:  fbt cluster down --run-id %s\n",
				e.RunID, e.Workspace, e.RunID)
			continue
		}
		if w, ok := rt.runOf(e.Workspace); ok {
			if err := w.Archive(true); err != nil {
				fmt.Fprintf(stderr, "fbt: %v\n", err)
			}
		}
		if err := rt.Registry.Release(e.RunID); err != nil {
			return err
		}
		fmt.Fprintf(stderr, "reclaimed the leftover cluster of run %s\n", e.RunID)
	}
	stale, err := rt.Registry.Reclaim()
	if err != nil {
		return err
	}
	for _, e := range stale {
		if w, ok := rt.runOf(e.Workspace); ok {
			if err := w.Archive(true); err != nil {
				fmt.Fprintf(stderr, "fbt: %v\n", err)
			}
		}
	}
	return nil
}
