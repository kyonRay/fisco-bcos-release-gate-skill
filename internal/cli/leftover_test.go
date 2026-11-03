package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kyonRay/fisco-bcos-release-gate-skill/internal/exitcode"
)

// leftoverGate is a gate.sh stub that behaves like a bring-up which died after start_all.sh: it
// lays the cluster out under the -o workspace, starts one long-lived process whose argv names the
// workspace and the node binary (what DiscoverNodes looks for) in its own session so the runner's
// process-group teardown does not take it down -- exactly as a real node survives -- and then
// fails with infra_error.
//
// The fake node is `sh <workspace>/127.0.0.1/node0/fisco-bcos` rather than a renamed sleep: a
// copied binary's argv[0] is not reliably the path it was launched by (macOS shows a copied
// /bin/sleep as plain "sleep"), while a script's path is an argument the kernel leaves alone.
const leftoverGate = `ws="$4"
mkdir -p "$ws/127.0.0.1/node0"
printf '#!/bin/sh\ntrap '"'"'kill $child'"'"' TERM INT\nsleep 300 & child=$!\nwait $child\n' > "$ws/127.0.0.1/node0/fisco-bcos"
printf '#!/bin/sh\npkill -f "$(cd "$(dirname "$0")" && pwd)/node0/fisco-bcos"; true\n' > "$ws/127.0.0.1/stop_all.sh"
chmod +x "$ws/127.0.0.1/node0/fisco-bcos" "$ws/127.0.0.1/stop_all.sh"
( perl -MPOSIX -e 'POSIX::setsid(); exec @ARGV' sh "$ws/127.0.0.1/node0/fisco-bcos" >/dev/null 2>&1 & )
sleep 1
event_set_outcome infra_error
exit 1
`

func stopLeftover(t *testing.T, ws string) {
	t.Helper()
	t.Cleanup(func() { _ = exec.Command("sh", filepath.Join(ws, "127.0.0.1", "stop_all.sh")).Run() })
}

// The first 3.18.0 run: apply_profile.sh failed after the nodes were up, gate run released the
// reservation anyway, and four processes held the port band with no fbt command able to name them.
func TestAChainLeftRunningByAFailedBringUpKeepsItsReservation(t *testing.T) {
	if _, err := exec.LookPath("perl"); err != nil {
		t.Skip("perl is needed to start the fake node in its own session")
	}
	_, flags := gateFixture(t, leftoverGate, cleanCase)

	code, out, errOut := runReal(t, append([]string{"--output", "json"},
		append(flags, "gate", "run", "-p", "default-latest", "--scenarios", "malformed")...)...)
	if code != exitcode.Infra {
		t.Fatalf("code = %v, want 30 (%s)", code, errOut)
	}
	var doc GateDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	ws := filepath.Join(doc.Workspace, "cluster")
	stopLeftover(t, ws)
	if !doc.ClusterKept || !strings.Contains(errOut, "still running") {
		t.Errorf("the run must say its chain is still up: kept=%v stderr=%q", doc.ClusterKept, errOut)
	}

	// It is listed, with its node recorded, so liveness and reclaim have something to work on.
	code, out, errOut = runReal(t, append([]string{"--output", "json"}, append(flags, "cluster", "ls")...)...)
	if code != exitcode.OK {
		t.Fatalf("cluster ls: %v (%s)", code, errOut)
	}
	var ls clusterDoc
	if err := json.Unmarshal([]byte(out), &ls); err != nil {
		t.Fatal(err)
	}
	if len(ls.Clusters) != 1 || ls.Clusters[0].RunID != doc.RunID || len(ls.Clusters[0].Nodes) == 0 {
		t.Fatalf("clusters = %+v; the leftover must stay registered with its nodes", ls.Clusters)
	}

	// And `cluster down --run-id` is the way out.
	if code, _, e := runReal(t, append(flags, "cluster", "down", "--run-id", doc.RunID)...); code != exitcode.OK {
		t.Fatalf("cluster down: %v (%s)", code, e)
	}
	code, out, _ = runReal(t, append([]string{"--output", "json"}, append(flags, "cluster", "ls")...)...)
	if err := json.Unmarshal([]byte(out), &ls); err != nil || len(ls.Clusters) != 0 {
		t.Errorf("after cluster down the registry still lists %+v (code %v)", ls.Clusters, code)
	}
}

// A workspace build_chain laid out that nobody registered is not ambiguous: `cluster down
// --workspace` stops exactly that directory.
func TestClusterDownStopsAnUnregisteredWorkspace(t *testing.T) {
	ti, flags := gateFixture(t, cleanGate, cleanCase)
	ws := filepath.Join(ti.Root, "orphan")
	if err := os.MkdirAll(filepath.Join(ws, "127.0.0.1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "127.0.0.1", "stop_all.sh"), []byte("#!/bin/sh\ntrue\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runReal(t, append(flags, "cluster", "down", "--workspace", ws)...)
	if code != exitcode.OK || !strings.Contains(out, "unregistered") {
		t.Errorf("code = %v out = %q err = %q; want 0 and the (unregistered) marker", code, out, errOut)
	}
	// A directory that is not a cluster is still refused: nothing to run scripts under.
	if code, _, _ := runReal(t, append(flags, "cluster", "down", "--workspace", ti.Root)...); code == exitcode.OK {
		t.Error("an arbitrary directory was accepted as a cluster workspace")
	}
}
