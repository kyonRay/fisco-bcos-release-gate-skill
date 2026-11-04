package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kyonRay/fisco-bcos-release-gate-skill/internal/clusters"
	"github.com/kyonRay/fisco-bcos-release-gate-skill/internal/exitcode"
	"github.com/kyonRay/fisco-bcos-release-gate-skill/internal/testinstall"
	"github.com/kyonRay/fisco-bcos-release-gate-skill/internal/workspace"
)

func stopLeftover(t *testing.T, ws string) {
	t.Helper()
	t.Cleanup(func() { _ = exec.Command("sh", filepath.Join(ws, "127.0.0.1", "stop_all.sh")).Run() })
}

// liveNode starts a fake node the way a real one lives: in its own session, surviving the runner's
// process-group teardown. The fake is `sh <workspace>/127.0.0.1/node0/fisco-bcos` rather than a
// renamed sleep: a copied binary's argv[0] is not reliably the path it was launched by (macOS shows
// a copied /bin/sleep as plain "sleep"), while a script's path is an argument the kernel leaves
// alone. Its first line takes the workspace from gate.sh's `-p P -o WS` arguments.
const liveNode = `ws="$4"
mkdir -p "$ws/127.0.0.1/node0/log" "$ws/127.0.0.1/node0/data"
echo log > "$ws/127.0.0.1/node0/log/log_1.log"
echo sst > "$ws/127.0.0.1/node0/data/1.sst"
printf '#!/bin/sh\ntrap '"'"'kill $child'"'"' TERM INT\nsleep 300 & child=$!\nwait $child\n' > "$ws/127.0.0.1/node0/fisco-bcos"
printf '#!/bin/sh\npkill -f "$(cd "$(dirname "$0")" && pwd)/node0/fisco-bcos"; true\n' > "$ws/127.0.0.1/stop_all.sh"
chmod +x "$ws/127.0.0.1/node0/fisco-bcos" "$ws/127.0.0.1/stop_all.sh"
( perl -MPOSIX -e 'POSIX::setsid(); exec @ARGV' sh "$ws/127.0.0.1/node0/fisco-bcos" >/dev/null 2>&1 & )
sleep 1
`

// liveNodeLayout is liveNode run on its own, for laying out a leftover cluster directly.
const liveNodeLayout = liveNode

func needPerl(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("perl"); err != nil {
		t.Skip("perl is needed to start the fake node in its own session")
	}
}

func registered(t *testing.T, ti testinstall.Install) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(ti.State, "clusters"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	var ids []string
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".json" {
			ids = append(ids, e.Name())
		}
	}
	return ids
}

func nodesUnder(ws string) int {
	return len(clusters.DiscoverNodes(ws, clusters.OSProber{}))
}

func gateDoc(t *testing.T, flags []string, script ...string) (GateDoc, exitcode.Code, string) {
	t.Helper()
	args := append(append([]string{"--output", "json"}, flags...), "gate", "run", "-p", "default-latest", "--scenarios", "malformed")
	code, out, errOut := runReal(t, append(args, script...)...)
	var doc GateDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("%v in %q (%s)", err, out, errOut)
	}
	return doc, code, errOut
}

// The first 3.18.0 run: apply_profile.sh failed after the nodes were up, and four processes held
// the port band after fbt exited. Whatever way a run ends, nothing of it may keep running.
func TestAFailedBringUpLeavesNothingRunningOrOnDisk(t *testing.T) {
	needPerl(t)
	ti, flags := gateFixture(t, liveNode+"event_set_outcome infra_error\nexit 1\n", cleanCase)
	doc, code, errOut := gateDoc(t, flags)
	ws := filepath.Join(doc.Workspace, "cluster")
	stopLeftover(t, ws)
	if code != exitcode.Infra {
		t.Fatalf("code = %v, want 30: the teardown must not change the verdict (%s)", code, errOut)
	}
	if doc.ClusterKept || nodesUnder(ws) != 0 {
		t.Errorf("nodes left: kept=%v running=%d", doc.ClusterKept, nodesUnder(ws))
	}
	if _, err := os.Stat(ws); !os.IsNotExist(err) {
		t.Errorf("the cluster directory survived: %v", err)
	}
	if ids := registered(t, ti); len(ids) != 0 {
		t.Errorf("registry = %v, the ports must be free", ids)
	}
	// A failed run keeps what explains the failure, and nothing bulky.
	if _, err := os.Stat(filepath.Join(doc.Workspace, "evidence/127.0.0.1/node0/log/log_1.log")); err != nil {
		t.Errorf("the failed run's log was not kept: %v", err)
	}
	if _, err := os.Stat(filepath.Join(doc.Workspace, "evidence/127.0.0.1/node0/data")); !os.IsNotExist(err) {
		t.Errorf("chain data was kept: %v", err)
	}
}

func TestAPassingRunKeepsOnlyItsTranscript(t *testing.T) {
	needPerl(t)
	ti, flags := gateFixture(t, liveNode+"bash \"$ws/127.0.0.1/stop_all.sh\"\nevent_set_outcome pass\n", cleanCase)
	doc, code, errOut := gateDoc(t, flags)
	if code != exitcode.OK {
		t.Fatalf("code = %v (%s)", code, errOut)
	}
	left, err := os.ReadDir(doc.Workspace)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range left {
		if e.Name() != "events.jsonl" && e.Name() != "evidence" {
			t.Errorf("%s was left behind", e.Name())
		}
	}
	if logs, _ := os.ReadDir(filepath.Join(doc.Workspace, "evidence")); len(logs) != 0 {
		t.Errorf("a passing run kept evidence: %v", logs)
	}
	if ids := registered(t, ti); len(ids) != 0 {
		t.Errorf("registry = %v", ids)
	}
}

// failures.jsonl is written inside the directory the cleanup removes; fbt report has to find the
// defect afterwards, and the row's evidence path has to lead somewhere.
func TestDefectsAreReportedAfterTheClusterIsGone(t *testing.T) {
	needPerl(t)
	row := `printf '{"profile":"p","scenario":"s","oracle":"crash","severity":"high","desc":"d","repro":"r","evidence":"%s/127.0.0.1","version":"v","reported":false}\n' "$ws" > "$ws/failures.jsonl"
`
	_, flags := gateFixture(t, liveNode+row+"bash \"$ws/127.0.0.1/stop_all.sh\"\nevent_set_outcome gate_fail\nexit 1\n", cleanCase)
	doc, code, errOut := gateDoc(t, flags)
	if code != exitcode.GateFail {
		t.Fatalf("code = %v (%s)", code, errOut)
	}
	code, out, errOut := runReal(t, append(flags, "report", "--run-id", doc.RunID)...)
	if code != exitcode.OK {
		t.Fatalf("report: %v (%s)", code, errOut)
	}
	want := filepath.Join(doc.Workspace, "evidence", "127.0.0.1")
	if !strings.Contains(out, want) {
		t.Errorf("report = %q; want the defect with evidence %s", out, want)
	}
	if _, err := os.Stat(filepath.Join(want, "node0/log/log_1.log")); err != nil {
		t.Errorf("the defect's evidence path holds no log: %v", err)
	}
	// The ledger hint has to name the directory failures.jsonl is in now.
	hint := "report_defects.sh " + doc.Workspace + " --dry-run"
	if !strings.Contains(out, hint) {
		t.Errorf("report = %q; want the hint %q", out, hint)
	}
}

// --keep-cluster is the one way to look at a live chain afterwards. The engine has to be told, or
// gate.sh tears it down itself; and cluster down has to finish what the flag postponed.
func TestKeepClusterLeavesTheChainUntilClusterDown(t *testing.T) {
	needPerl(t)
	body := liveNode + `[ "${RG_KEEP_CLUSTER:-0}" = 1 ] || bash "$ws/127.0.0.1/stop_all.sh"
event_set_outcome pass
`
	ti, flags := gateFixture(t, body, cleanCase)
	doc, code, errOut := gateDoc(t, flags, "--keep-cluster")
	ws := filepath.Join(doc.Workspace, "cluster")
	stopLeftover(t, ws)
	if code != exitcode.OK {
		t.Fatalf("code = %v (%s)", code, errOut)
	}
	if !doc.ClusterKept || nodesUnder(ws) != 1 || !strings.Contains(errOut, "still running") {
		t.Fatalf("kept=%v running=%d stderr=%q", doc.ClusterKept, nodesUnder(ws), errOut)
	}
	// A kept chain has no owner: the run that kept it has exited, and the next one must not reap it.
	if code, _, e := runReal(t, append(flags, "case", "list")...); code != exitcode.OK {
		t.Fatalf("case list: %v (%s)", code, e)
	}
	r := clusters.New(filepath.Join(ti.State, "clusters"))
	if orphans, err := r.Orphans(); err != nil || len(orphans) != 0 {
		t.Fatalf("orphans = %v, %v", orphans, err)
	}
	code, _, e := runReal(t, append(flags, "cluster", "down", "--run-id", doc.RunID)...)
	if code != exitcode.OK {
		t.Fatalf("cluster down: %v (%s)", code, e)
	}
	if nodesUnder(ws) != 0 {
		t.Error("cluster down left a node running")
	}
	if _, err := os.Stat(ws); !os.IsNotExist(err) {
		t.Errorf("cluster down left the directory: %v", err)
	}
	if ids := registered(t, ti); len(ids) != 0 {
		t.Errorf("registry = %v", ids)
	}
}

// The same request made through the configuration has to work too: a key that is accepted and then
// ignored would tear down the chain somebody configured fbt to keep.
func TestKeepClusterThroughTheEnvironment(t *testing.T) {
	needPerl(t)
	body := liveNode + `[ "${RG_KEEP_CLUSTER:-0}" = 1 ] || bash "$ws/127.0.0.1/stop_all.sh"
event_set_outcome pass
`
	_, flags := gateFixture(t, body, cleanCase)
	t.Setenv("RG_KEEP_CLUSTER", "1")
	doc, code, errOut := gateDoc(t, flags)
	ws := filepath.Join(doc.Workspace, "cluster")
	stopLeftover(t, ws)
	if code != exitcode.OK || !doc.ClusterKept || nodesUnder(ws) != 1 {
		t.Errorf("code=%v kept=%v running=%d (%s)", code, doc.ClusterKept, nodesUnder(ws), errOut)
	}
}

// Only the last stage of a kept round may leave its chain up: gate run's later stages build their
// own chain on the same ports.
func TestKeepClusterAppliesToTheLastStageOnly(t *testing.T) {
	seen := `echo "$(basename "$0") keep=${RG_KEEP_CLUSTER:-unset}" >> "$FBT_STAGE_LOG"
event_set_outcome pass
`
	ti, flags := gateFixture(t, seen, seen)
	writeCase(t, ti, "a.case", "active")
	writeCase(t, ti, "b.case", "active")
	stages := filepath.Join(t.TempDir(), "stages")
	t.Setenv("FBT_STAGE_LOG", stages)
	code, _, e := runReal(t, append(flags, "gate", "run", "-p", "default-latest", "--keep-cluster")...)
	if code != exitcode.OK {
		t.Fatalf("code = %v (%s)", code, e)
	}
	raw, err := os.ReadFile(stages)
	if err != nil {
		t.Fatal(err)
	}
	want := "gate.sh keep=0\nrun_case.sh keep=0\nrun_case.sh keep=1\n"
	if string(raw) != want {
		t.Errorf("stages =\n%swant\n%s", raw, want)
	}
}

// fbt killed with -9 cleans nothing up. The next run is what is left to do it, and it has to do so
// before its port check, because the orphan's nodes are what holds those ports.
func TestTheNextRunReapsTheChainOfAKilledRun(t *testing.T) {
	needPerl(t)
	ti, flags := gateFixture(t, liveNode+"event_set_outcome infra_error\nexit 1\n", "event_set_outcome pass\n")
	// Lay out a killed run by hand: a live node, a registry entry, an owner that no longer exists.
	dead := exec.Command("true")
	if err := dead.Run(); err != nil {
		t.Fatal(err)
	}
	w, err := workspace.Create(ti.State, "killed-run")
	if err != nil {
		t.Fatal(err)
	}
	start := exec.Command("bash", "-c", liveNodeLayout, "_", "-p", "p", "-o", w.Cluster)
	if out, err := start.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	stopLeftover(t, w.Cluster)
	r := clusters.New(filepath.Join(ti.State, "clusters"))
	if _, err := r.Reserve(clusters.Entry{
		RunID: "killed-run", Workspace: w.Cluster,
		Ports: []clusters.PortRange{{Name: "p2p", Base: 46000, Count: 1}},
		Owner: &clusters.Node{Name: "fbt", PID: dead.Process.Pid},
	}, false); err != nil {
		t.Fatal(err)
	}
	if nodesUnder(w.Cluster) != 1 {
		t.Fatal("the fake orphan is not running")
	}
	writeCase(t, ti, "a.case", "active")
	code, _, errOut := runReal(t, append(flags, "case", "run", "a.case")...)
	if code != exitcode.OK {
		t.Fatalf("code = %v (%s)", code, errOut)
	}
	if nodesUnder(w.Cluster) != 0 {
		t.Error("the orphan's node is still running")
	}
	if _, err := os.Stat(w.Cluster); !os.IsNotExist(err) {
		t.Errorf("the orphan's directory survived: %v", err)
	}
	if _, err := os.Stat(filepath.Join(w.Evidence, "127.0.0.1/node0/log/log_1.log")); err != nil {
		t.Errorf("the orphan's log was not kept: %v", err)
	}
	if ids := registered(t, ti); len(ids) != 0 {
		t.Errorf("registry = %v", ids)
	}
}

// A chain somebody started with cluster up has no owner; a later run must leave it alone.
func TestALiveOwnerlessChainIsNotReaped(t *testing.T) {
	needPerl(t)
	ti, flags := gateFixture(t, cleanGate, "event_set_outcome pass\n")
	w, err := workspace.Create(ti.State, "kept-run")
	if err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("bash", "-c", liveNodeLayout, "_", "-p", "p", "-o", w.Cluster).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	stopLeftover(t, w.Cluster)
	r := clusters.New(filepath.Join(ti.State, "clusters"))
	if _, err := r.Reserve(clusters.Entry{
		RunID: "kept-run", Workspace: w.Cluster,
		Ports: []clusters.PortRange{{Name: "p2p", Base: 46000, Count: 1}},
		Nodes: clusters.DiscoverNodes(w.Cluster, clusters.OSProber{}),
	}, false); err != nil {
		t.Fatal(err)
	}
	writeCase(t, ti, "a.case", "active")
	if code, _, e := runReal(t, append(flags, "case", "run", "a.case")...); code != exitcode.OK {
		t.Logf("case run: %v (%s)", code, e) // refused by the single-instance lock is fine
	}
	if nodesUnder(w.Cluster) != 1 {
		t.Error("a chain left running on purpose was stopped")
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
