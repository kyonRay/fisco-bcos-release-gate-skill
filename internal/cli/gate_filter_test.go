package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kyonRay/fisco-bcos-release-gate-skill/internal/exitcode"
)

// Spec §6.2: naming either stage means the OTHER stage is empty. Before this, `--scenarios
// malformed` ran malformed and then built a chain per active fixture anyway.
func TestScenariosOnlyRoundDoesNotSweepTheCases(t *testing.T) {
	ti, flags := gateFixture(t, cleanGate, cleanCase)
	writeCase(t, ti, "live.case", "active")
	marker := filepath.Join(ti.Root, "case-ran")
	stubEngine(t, ti, cleanGate, "touch "+marker+"\n"+cleanCase)

	code, out, errOut := runReal(t, append([]string{"--output", "json"},
		append(flags, "gate", "run", "-p", "default-latest", "--scenarios", "malformed")...)...)
	if code != exitcode.OK {
		t.Fatalf("code = %v (%s)", code, errOut)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("--scenarios alone still ran the case sweep; the unmentioned stage must be empty")
	}
	var doc GateDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Cases) != 0 {
		t.Errorf("cases = %+v; a scenarios-only round reports no case stage", doc.Cases)
	}
}

func TestCasesOnlyRoundDoesNotRunTheScenarioFamilies(t *testing.T) {
	ti, flags := gateFixture(t, cleanGate, cleanCase)
	writeCase(t, ti, "live.case", "active")
	writeCase(t, ti, "other.case", "active")
	marker := filepath.Join(ti.Root, "gate-ran")
	stubEngine(t, ti, "touch "+marker+"\n"+cleanGate, cleanCase)

	code, out, errOut := runReal(t, append([]string{"--output", "json"},
		append(flags, "gate", "run", "-p", "default-latest", "--cases", "live*")...)...)
	if code != exitcode.OK {
		t.Fatalf("code = %v (%s)", code, errOut)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("--cases alone still ran gate.sh; a cases-only round builds no chain for the families")
	}
	var doc GateDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Cases) != 1 {
		t.Errorf("cases = %+v; the glob selects live.case and nothing else", doc.Cases)
	}
	if len(doc.Scenarios) != 0 {
		t.Errorf("scenarios = %+v; no family ran", doc.Scenarios)
	}
}

// Spec §6.2: an empty match is a usage error, not an empty (and therefore green) sweep.
func TestACasesGlobThatMatchesNothingActiveIsAUsageError(t *testing.T) {
	ti, flags := gateFixture(t, cleanGate, cleanCase)
	writeCase(t, ti, "known.case", "pending")

	code, _, errOut := runReal(t, append(flags, "gate", "run", "-p", "default-latest", "--cases", "known*")...)
	if code != exitcode.Config {
		t.Fatalf("code = %v, want 20 for a glob that matches only a pending fixture (%s)", code, errOut)
	}
	if !strings.Contains(errOut, "pending") {
		t.Errorf("the refusal must name the pending fixture it matched: %s", errOut)
	}
	code, _, errOut = runReal(t, append(flags, "gate", "run", "-p", "default-latest", "--cases", "nothing*")...)
	if code != exitcode.Config || !strings.Contains(errOut, "matches no case") {
		t.Errorf("code = %v, err = %q; want 20 and a message that says nothing matched", code, errOut)
	}
}

// The reserved flag is refused before anything else is resolved, so the refusal is what the
// operator reads -- not a profile error that happened to come first. plan refuses it too.
func TestWithFuzzIsRefusedBeforeTheProfileIsResolved(t *testing.T) {
	_, flags := gateFixture(t, cleanGate, cleanCase)
	code, _, errOut := runReal(t, append(flags, "gate", "run", "-p", "no-such-profile", "--with-fuzz")...)
	if code != exitcode.Config || !strings.Contains(errOut, "--with-fuzz") {
		t.Errorf("run: code = %v, err = %q; want 20 naming --with-fuzz", code, errOut)
	}
	code, _, errOut = runReal(t, append(flags, "gate", "plan", "-p", "default-latest", "--with-fuzz")...)
	if code != exitcode.Config || !strings.Contains(errOut, "--with-fuzz") {
		t.Errorf("plan: code = %v, err = %q; want 20 naming --with-fuzz", code, errOut)
	}
}

func TestPlanShowsWhyAFilteredRoundSkipsAFixture(t *testing.T) {
	ti, flags := gateFixture(t, cleanGate, cleanCase)
	writeCase(t, ti, "live.case", "active")

	code, out, errOut := runReal(t, append([]string{"--output", "json"},
		append(flags, "gate", "plan", "-p", "default-latest", "--scenarios", "malformed")...)...)
	if code != exitcode.OK {
		t.Fatalf("code = %v (%s)", code, errOut)
	}
	var doc PlanDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Cases) != 1 || doc.Cases[0].Swept || !strings.Contains(doc.Cases[0].Reason, "--scenarios") {
		t.Errorf("cases = %+v; the active fixture is skipped, and the plan says it is because of --scenarios", doc.Cases)
	}

	code, out, errOut = runReal(t, append([]string{"--output", "json"},
		append(flags, "gate", "plan", "-p", "default-latest", "--cases", "live*")...)...)
	if code != exitcode.OK {
		t.Fatalf("code = %v (%s)", code, errOut)
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Scenarios) != 0 || len(doc.Cases) != 1 || !doc.Cases[0].Swept {
		t.Errorf("plan = %+v; a cases-only round lists no family and sweeps the match", doc)
	}
}
