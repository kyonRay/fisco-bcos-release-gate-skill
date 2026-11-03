package cli

import (
	"fmt"
	"io"

	"github.com/kyonRay/fisco-bcos-release-gate-skill/internal/clusters"
)

// releaseUnlessAlive ends a run's reservation -- unless the chain it built is still running.
//
// gate run and case run used to release unconditionally on the way out, on the assumption that
// the engine tears its cluster down. It does on the normal path. When apply_profile.sh fails
// after start_all.sh (the first 3.18.0 run: a write landed on a port that was not open yet), the
// nodes stay up, the reservation is gone, `cluster ls` shows nothing, and `cluster down
// --workspace` finds no registration: four processes holding the port band with no fbt command
// able to name them. The next run is then refused by the bind probe with no way to see why.
//
// So the processes are asked, not the exit code: if any node of this workspace is alive, the
// entry is kept (with the PIDs recorded, so liveness and reclaim work on it) and the operator is
// told how to stop it. Returns true when the cluster was kept.
func releaseUnlessAlive(rt *runtime, stderr io.Writer) bool {
	nodes := clusters.DiscoverNodes(rt.Workspace.Cluster, clusters.OSProber{})
	if len(nodes) == 0 {
		_ = rt.Registry.Release(rt.Norm.RunID)
		return false
	}
	if entry, err := rt.Registry.Resolve(rt.Norm.RunID, ""); err == nil {
		entry.Nodes = nodes
		_ = rt.Registry.Update(entry)
	}
	fmt.Fprintf(stderr, "cluster %s is still running (%d node processes under %s); its ports stay reserved.\n"+
		"  stop it with:  fbt cluster down --run-id %s\n", rt.Norm.RunID, len(nodes), rt.Workspace.Cluster, rt.Norm.RunID)
	return true
}
