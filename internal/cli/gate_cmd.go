package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/kyonRay/fisco-bcos-release-gate-skill/internal/cases"
	"github.com/kyonRay/fisco-bcos-release-gate-skill/internal/clusters"
	"github.com/kyonRay/fisco-bcos-release-gate-skill/internal/doctor"
	"github.com/kyonRay/fisco-bcos-release-gate-skill/internal/events"
	"github.com/kyonRay/fisco-bcos-release-gate-skill/internal/execution"
	"github.com/kyonRay/fisco-bcos-release-gate-skill/internal/exitcode"
	"github.com/kyonRay/fisco-bcos-release-gate-skill/internal/fbterr"
)

func init() { commands["gate"] = gateCommand }

// gateCommand routes the three things you can do with a gate: run one, print what one WOULD do,
// and drive the upgrade timeline. They live under `gate` rather than at the top level (spec §6.3)
// because all three take the same profile and produce a verdict about the same chain; `fbt plan`
// on its own said nothing about what it was planning.
func gateCommand(ctx context.Context, o GlobalOptions, args []string, stdout, stderr io.Writer) exitcode.Code {
	if len(args) == 0 {
		return emitError(stdout, stderr, o.Output,
			fbterr.Configf("gate needs a subcommand: run, plan or upgrade"))
	}
	switch args[0] {
	case "run":
		return gateRun(ctx, o, args[1:], stdout, stderr)
	case "plan":
		return planCommand(ctx, o, args[1:], stdout, stderr)
	case "upgrade":
		return upgradeRun(ctx, o, args[1:], stdout, stderr)
	}
	return emitError(stdout, stderr, o.Output,
		fbterr.Configf("unknown subcommand %q; gate takes run, plan or upgrade", args[0]))
}

// gateOptions are the flags shared by `gate run` and `plan`, so the plan is guaranteed to describe
// the run it claims to.
type gateOptions struct {
	profile     string
	scenarios   string
	cases       string
	casesSet    bool // --cases was given, even as an empty string
	failFast    bool
	parallel    bool
	keepCluster bool
	// withFuzz is spec §6.3's reserved flag. It exists so the name is CLAIMED and using it is an
	// explicit refusal rather than "flag provided but not defined", which reads like a typo.
	withFuzz bool
}

// parse binds, parses, and records which filter flags were PRESENT: spec §6.2 keys on presence,
// so `--cases ""` is an empty match (exit 20), not "no filter".
func (g *gateOptions) parse(fs *flag.FlagSet, args []string) error {
	g.bind(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "cases" {
			g.casesSet = true
		}
	})
	return nil
}

func (g *gateOptions) bind(fs *flag.FlagSet) {
	fs.StringVar(&g.profile, "p", "production-enterprise", "profile name or path")
	fs.StringVar(&g.scenarios, "scenarios", "", "comma-separated scenario families (default: all)")
	fs.BoolVar(&g.failFast, "fail-fast", false, "stop at the first gate failure too")
	fs.BoolVar(&g.parallel, "allow-parallel", false,
		"allow a second cluster, if its port ranges do not overlap")
	fs.StringVar(&g.cases, "cases", "",
		"basename glob of the .case fixtures to replay (default: every active one)")
	fs.BoolVar(&g.keepCluster, "keep-cluster", false,
		"leave the chain and its directory in place; fbt cluster down ends them")
	fs.BoolVar(&g.withFuzz, "with-fuzz", false,
		"reserved: fold a fuzz pass into the round (not implemented, returns 20)")
}

// roundPlan is what one invocation will actually do, after spec §6.2's filter table has been
// applied to the two stages.
type roundPlan struct {
	// scenarios is the explicit --scenarios list; empty with allScenarios set means "all four",
	// empty without it means the scenario stage does not run at all.
	scenarios    []string
	allScenarios bool
	sweep        []cases.Case
}

// round applies spec §6.2: no filter means both stages in full; naming either stage means the
// OTHER stage is empty. `--scenarios malformed` is a request for malformed and nothing else, not
// for malformed plus every fixture -- the sweep used to run anyway, so a round that was asked for
// one family still built a chain per active case afterwards.
func (g gateOptions) round(all []cases.Case) (roundPlan, error) {
	sel := splitList(g.scenarios)
	switch {
	case len(sel) == 0 && !g.casesSet:
		return roundPlan{allScenarios: true, sweep: cases.Sweep(all)}, nil
	case !g.casesSet:
		return roundPlan{scenarios: sel}, nil
	case g.cases == "":
		return roundPlan{}, fbterr.Configf("--cases needs a pattern; an empty one matches nothing (spec §6.2)")
	}
	sweep, err := cases.Select(all, g.cases)
	if err != nil {
		return roundPlan{}, err
	}
	return roundPlan{scenarios: sel, sweep: sweep}, nil
}

func (r roundPlan) runsScenarios() bool { return r.allScenarios || len(r.scenarios) > 0 }

// skipReason explains, for the plan, why a fixture is not in this round's sweep.
func (r roundPlan) skipReason(g gateOptions, c cases.Case) string {
	switch {
	case c.Status == cases.StatusPending:
		return "pending: the defect is not fixed, so a sweep would be permanently red"
	case c.Status == cases.StatusExample:
		return "example: a format demonstration, never swept"
	case g.casesSet:
		return "not matched by --cases " + g.cases
	case !r.allScenarios:
		return "not selected: --scenarios names the whole round (spec §6.2)"
	}
	return ""
}

// refuseWithFuzz is spec §6.3's reserved flag. It is checked before anything is resolved so that
// the refusal is what the operator sees, not a profile or config error that happens to come first.
func refuseWithFuzz(g gateOptions) error {
	if !g.withFuzz {
		return nil
	}
	return fbterr.Configf("--with-fuzz is reserved and not implemented; run the fuzz pass " +
		"separately with `fbt cluster up` followed by `fbt fuzz run --attach <run-id>`")
}

// GateDoc is spec §8's success document. UI and CI build against this shape.
type GateDoc struct {
	Exit      int          `json:"exit"`
	RunID     string       `json:"run_id"`
	Workspace string       `json:"workspace"`
	Profile   string       `json:"profile"`
	Scenarios []stageEntry `json:"scenarios"`
	Cases     []stageEntry `json:"cases"`
	Oracles   []oracleHit  `json:"oracles"`
	Reason    string       `json:"reason,omitempty"`
	// ClusterKept is set when the run ended with its nodes still running (--keep-cluster, or a
	// teardown that failed): the reservation stands, and `fbt cluster down --run-id` stops it.
	ClusterKept bool `json:"cluster_kept,omitempty"`
}

type stageEntry struct {
	Name   string `json:"name"`
	Result string `json:"result"`
	Skip   string `json:"skip_reason,omitempty"`
}

type oracleHit struct {
	Oracle  string `json:"oracle"`
	Phase   string `json:"phase,omitempty"`
	Verdict string `json:"verdict"`
}

// gateRun is the whole sweep: dependency check, port reservation, the scenario families, then the
// .case fixtures.
func gateRun(ctx context.Context, o GlobalOptions, args []string, stdout, stderr io.Writer) exitcode.Code {
	fs := flag.NewFlagSet("gate run", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	finish := RegisterGlobalFlags(fs, &o)
	var g gateOptions
	if err := g.parse(fs, args); err != nil {
		return emitError(stdout, stderr, o.Output, fbterr.Configf("%v", err))
	}
	if err := finish(); err != nil {
		return emitError(stdout, stderr, o.Output, err)
	}
	if err := refuseWithFuzz(g); err != nil {
		return emitError(stdout, stderr, o.Output, err)
	}
	rt, err := newRuntime(o, g.profile, stdout, stderr)
	if err != nil {
		return emitError(stdout, stderr, o.Output, err)
	}

	// 1. Everything that can be refused without side effects, refused first. Each of these leaves
	// nothing behind, and every one of them AFTER a cluster is up would strand it.
	//
	// Every .case parses before anything starts, so one typo is a message rather than a failure
	// discovered with a chain already running (spec §6.4).
	allCases, err := rt.Cases.List()
	if err != nil {
		return emitError(stdout, stderr, o.Output, err)
	}
	round, err := g.round(allCases)
	if err != nil {
		return emitError(stdout, stderr, o.Output, err)
	}
	// A round that runs no scenario family needs what a case replay needs, not Viem and the UT
	// binaries: checking dependencies of work that was not selected is how a tool refuses to run
	// for reasons that have nothing to do with the request.
	plan, err := roundDeps(round)
	if err != nil {
		return emitError(stdout, stderr, o.Output, err)
	}
	if _, err := doctor.CheckIn(rt.Exec.RepoRoot, plan, rt.Values, doctor.OSProbe{}); err != nil {
		return emitError(stdout, stderr, o.Output, err)
	}
	selected, sweep := round.scenarios, round.sweep
	ports, err := derivePorts(rt.Values)
	if err != nil {
		return emitError(stdout, stderr, o.Output, err)
	}
	// Before the port check: a leftover from a killed run is what would be holding those ports, and
	// a run refused because a machine crashed last week would be a permanent block.
	if err := reapLeftovers(o, stderr); err != nil {
		return emitError(stdout, stderr, o.Output, err)
	}
	// The registry only knows the clusters fbt built, so ask the operating system about the rest.
	// Skipping this cost ninety seconds and a 651MB binary copy before four nodes died on
	// "Address already in use".
	if err := clusters.CheckFree(ports); err != nil {
		return emitError(stdout, stderr, o.Output, err)
	}

	// 2. Reserve.
	if err := rt.begin(map[string]interface{}{"profile": g.profile}); err != nil {
		return rt.fail(stdout, stderr, o.Output, err)
	}
	if _, err := rt.Registry.Reserve(clusters.Entry{
		RunID:     rt.Norm.RunID,
		Workspace: rt.Workspace.Cluster,
		Profile:   g.profile,
		Ports:     ports,
		Owner:     clusters.Self(),
	}, g.parallel); err != nil {
		return rt.fail(stdout, stderr, o.Output, err)
	}
	doc := GateDoc{RunID: rt.Norm.RunID, Workspace: rt.Workspace.Root, Profile: g.profile}
	g.keepCluster = rt.keepCluster(g.keepCluster)
	defer rt.settle(g.keepCluster, stderr) // an early return still leaves nothing behind

	// 3. Stage one: the scenario families. Skipped entirely when the round is cases-only
	// (spec §6.2): gate.sh would build a chain to run nothing on.
	var first execution.Result
	if round.runsScenarios() {
		gateArgs := []string{"-p", rt.ProfilePath, "-o", rt.Workspace.Cluster}
		if len(selected) > 0 {
			gateArgs = append(gateArgs, "--scenarios", strings.Join(selected, ","))
		}
		first = rt.Exec.Run(ctx, execution.Command{
			Script:   "gate.sh",
			Args:     gateArgs,
			Requires: []string{"gate"},
			Config:   rt.stageConfig(g.keepCluster && len(sweep) == 0),
			Timeout:  rt.timeout(),
		})
	}

	// 4. Stage two: the .case sweep, subject to the continue policy.
	for i, c := range sweep {
		if stop, why := stopSweep(rt.Agg.Result(), g.failFast); stop {
			doc.Cases = append(doc.Cases, stageEntry{Name: c.Name, Result: "skip", Skip: why})
			continue
		}
		rt.Exec.Run(ctx, execution.Command{
			Script:   "run_case.sh",
			Args:     []string{c.Path, "-o", rt.Workspace.Cluster},
			Requires: []string{"case"},
			Config:   rt.stageConfig(g.keepCluster && i == len(sweep)-1),
			Timeout:  rt.timeout(),
		})
	}

	// Before the document is rendered, so the JSON says whether a chain was left behind.
	code, kept := rt.settle(g.keepCluster, stderr)
	doc.ClusterKept = kept

	// The report is derived from the normalized event stream, not assembled alongside it. A second
	// source of truth here is how a summary comes to disagree with the events it summarizes.
	doc.Exit = code.Int()
	doc.Scenarios = collectStages(rt.events, "scenario_finished", "name", "result")
	doc.Cases = append(doc.Cases, collectStages(rt.events, "case_replayed", "file", "result")...)
	doc.Oracles = collectOracles(rt.events)
	if first.Reason != "" {
		doc.Reason = first.Reason
	}

	if err := render(stdout, o.Output, doc, func(w io.Writer) {
		writeGateHuman(w, doc)
	}); err != nil {
		return rt.fail(stdout, stderr, o.Output, err)
	}
	return code
}

// roundDeps picks the dependency table for what the round actually runs: the gate table narrowed
// to the selected families when any family runs, the case table otherwise.
func roundDeps(r roundPlan) ([]doctor.Requirement, error) {
	if r.runsScenarios() {
		return doctor.Plan("gate", r.scenarios)
	}
	return doctor.Plan("case", nil)
}

// stopSweep implements spec §10's continue policy.
//
// A gate failure is a RESULT, and stopping at the first one throws away the rest of the picture
// the operator came for. A 20/30/40 is different: the configuration, the machine or fbt itself is
// broken, so every later case would be measuring something other than the chain.
func stopSweep(worst exitcode.Code, failFast bool) (bool, string) {
	switch worst {
	case exitcode.Config, exitcode.Infra, exitcode.Host, exitcode.Canceled:
		return true, "earlier_" + strings.ToLower(worst.String())
	case exitcode.GateFail:
		if failFast {
			return true, "fail_fast"
		}
	}
	return false, ""
}

func collectStages(evs []events.Event, name, keyField, resultField string) []stageEntry {
	var out []stageEntry
	for _, e := range evs {
		if e.Ev != name {
			continue
		}
		out = append(out, stageEntry{
			Name:   payloadString(e, keyField),
			Result: payloadString(e, resultField),
			Skip:   payloadString(e, "skip_reason"),
		})
	}
	return out
}

// collectOracles keeps only the trips. A clean round produces one oracle_check per oracle per
// phase, and listing every one of them buries the handful that matter.
func collectOracles(evs []events.Event) []oracleHit {
	var out []oracleHit
	for _, e := range evs {
		switch e.Ev {
		case "oracle_check":
			if payloadString(e, "verdict") != "trip" {
				continue
			}
			out = append(out, oracleHit{
				Oracle: payloadString(e, "oracle"), Phase: payloadString(e, "phase"),
				Verdict: "trip",
			})
		case "trip":
			out = append(out, oracleHit{Oracle: payloadString(e, "oracle"), Verdict: "trip"})
		}
	}
	return out
}

func payloadString(e events.Event, field string) string {
	if v, ok := e.Payload[field].(string); ok {
		return v
	}
	return ""
}

func writeGateHuman(w io.Writer, doc GateDoc) {
	fmt.Fprintf(w, "run %s  profile %s\n%s\n\n", doc.RunID, doc.Profile, doc.Workspace)
	for _, s := range doc.Scenarios {
		fmt.Fprintf(w, "  scenario %-14s %s %s\n", s.Name, s.Result, s.Skip)
	}
	for _, c := range doc.Cases {
		fmt.Fprintf(w, "  case     %-14s %s %s\n", c.Name, c.Result, c.Skip)
	}
	for _, o := range doc.Oracles {
		fmt.Fprintf(w, "  ORACLE TRIP: %s %s\n", o.Oracle, o.Phase)
	}
	if doc.Exit == 0 {
		fmt.Fprintf(w, "\nGATE: PASS\n")
		return
	}
	fmt.Fprintf(w, "\nGATE: FAIL (exit %d) %s\n", doc.Exit, doc.Reason)
}

// derivePorts reads the cluster's port bases out of the resolved configuration. The registry needs
// them BEFORE the chain exists, because a reservation made after the ports are bound reserves
// nothing.
func derivePorts(values map[string]string) ([]clusters.PortRange, error) {
	n, err := intValue(values, "cluster.node_count", 4)
	if err != nil {
		return nil, err
	}
	p2p, err := intValue(values, "cluster.p2p_base_port", 30300)
	if err != nil {
		return nil, err
	}
	rpc, err := intValue(values, "cluster.bcos_base_port", 20200)
	if err != nil {
		return nil, err
	}
	web3, err := intValue(values, "cluster.web3_base_port", 8545)
	if err != nil {
		return nil, err
	}
	return clusters.DerivePorts(n, p2p, rpc, web3)
}

func intValue(values map[string]string, key string, fallback int) (int, error) {
	raw, ok := values[key]
	if !ok || raw == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fbterr.Configf("%s = %q is not a number", key, raw)
	}
	return n, nil
}

// PlanDoc is what `fbt gate plan` prints: everything the run would do, with nothing started.
type PlanDoc struct {
	Profile   string               `json:"profile"`
	Scenarios []string             `json:"scenarios"`
	Cases     []planCase           `json:"cases"`
	Ports     []clusters.PortRange `json:"ports"`
	Deps      []string             `json:"required_deps"`
}

type planCase struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Swept  bool   `json:"swept"`
	Reason string `json:"reason,omitempty"`
}

// planCommand answers "what would `gate run` actually do" without touching anything.
//
// It lists the SKIPPED cases too, with why. A plan that shows only what runs cannot answer the
// question people actually bring to it -- "why isn't my fixture being tested" -- which is the
// question a status field silently answers wrong when nobody can see it.
func planCommand(_ context.Context, o GlobalOptions, args []string, stdout, stderr io.Writer) exitcode.Code {
	fs := flag.NewFlagSet("gate plan", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	finish := RegisterGlobalFlags(fs, &o)
	var g gateOptions
	if err := g.parse(fs, args); err != nil {
		return emitError(stdout, stderr, o.Output, fbterr.Configf("%v", err))
	}
	if err := finish(); err != nil {
		return emitError(stdout, stderr, o.Output, err)
	}
	if err := refuseWithFuzz(g); err != nil {
		return emitError(stdout, stderr, o.Output, err)
	}
	rt, err := newRuntime(o, g.profile, stdout, stderr)
	if err != nil {
		return emitError(stdout, stderr, o.Output, err)
	}
	all, err := rt.Cases.List()
	if err != nil {
		return emitError(stdout, stderr, o.Output, err)
	}
	round, err := g.round(all)
	if err != nil {
		return emitError(stdout, stderr, o.Output, err)
	}
	selected := round.scenarios
	if round.allScenarios {
		selected = []string{"ut", "dual_rpc", "malformed", "jsd"}
	}
	ports, err := derivePorts(rt.Values)
	if err != nil {
		return emitError(stdout, stderr, o.Output, err)
	}
	deps, err := roundDeps(round)
	if err != nil {
		return emitError(stdout, stderr, o.Output, err)
	}

	doc := PlanDoc{Profile: g.profile, Scenarios: selected, Ports: ports}
	for _, d := range deps {
		doc.Deps = append(doc.Deps, d.Name)
	}
	swept := map[string]bool{}
	for _, c := range round.sweep {
		swept[c.Name] = true
	}
	for _, c := range all {
		pc := planCase{Name: c.Name, Status: string(c.Status), Swept: swept[c.Name]}
		if !pc.Swept {
			pc.Reason = round.skipReason(g, c)
		}
		doc.Cases = append(doc.Cases, pc)
	}

	if err := render(stdout, o.Output, doc, func(w io.Writer) {
		families := strings.Join(doc.Scenarios, ", ")
		if families == "" {
			families = "(none: --cases names the whole round, spec §6.2)"
		}
		fmt.Fprintf(w, "profile:   %s\nscenarios: %s\nports:     %s\n\n",
			doc.Profile, families, portList(doc.Ports))
		for _, c := range doc.Cases {
			mark := "skip"
			if c.Swept {
				mark = "run "
			}
			fmt.Fprintf(w, "  %s %-24s %s\n", mark, c.Name, c.Reason)
		}
	}); err != nil {
		return emitError(stdout, stderr, o.Output, err)
	}
	return exitcode.OK
}
