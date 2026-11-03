package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kyonRay/fisco-bcos-release-gate-skill/internal/exitcode"
)

// gate upgrade builds a chain on the configured band like a gate round; it used to do so without
// reserving the ports or keeping them when its nodes outlived it (the third real upgrade run left
// four nodes behind and `cluster ls` showed nothing).
func TestGateUpgradeReservesThePortsAndKeepsThemWhileNodesLive(t *testing.T) {
	ti, flags := gateFixture(t, cleanGate, cleanCase)
	// gate_upgrade.sh stub: same shape as the leftover gate stub, args are -p P --old-bin .. -o WS
	ti.WriteScript(t, "gate_upgrade.sh", `source "$(dirname "$0")/event_lib.sh"
event_begin_command gate_upgrade.sh
ws=""; while [ $# -gt 0 ]; do [ "$1" = -o ] && ws="$2"; shift; done
mkdir -p "$ws/127.0.0.1/node0"
printf '#!/bin/sh\ntrap '"'"'kill $child'"'"' TERM INT\nsleep 300 & child=$!\nwait $child\n' > "$ws/127.0.0.1/node0/fisco-bcos"
printf '#!/bin/sh\npkill -f "$(cd "$(dirname "$0")" && pwd)/node0/fisco-bcos"; true\n' > "$ws/127.0.0.1/stop_all.sh"
chmod +x "$ws/127.0.0.1/node0/fisco-bcos" "$ws/127.0.0.1/stop_all.sh"
( perl -MPOSIX -e 'POSIX::setsid(); exec @ARGV' sh "$ws/127.0.0.1/node0/fisco-bcos" >/dev/null 2>&1 & )
sleep 1
event_set_outcome gate_fail
exit 1
`)
	bin := filepath.Join(ti.Root, "repo", "fisco-bcos")
	code, _, errOut := runReal(t, append(flags, "gate", "upgrade", "-p", "default-latest",
		"--old-bin", bin, "--new-bin", bin, "--target-ver", "3.18.0")...)
	if code != exitcode.GateFail {
		t.Fatalf("code = %v, want 10 (%s)", code, errOut)
	}
	entries, err := os.ReadDir(filepath.Join(ti.State, "clusters"))
	if err != nil {
		t.Fatal(err)
	}
	var kept []string
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".json" { // the registry's entries; the lock file sits beside them
			kept = append(kept, e.Name())
		}
	}
	if len(kept) != 1 {
		t.Fatalf("registry entries = %v; the upgrade's reservation must survive while its nodes live", kept)
	}
	ws := filepath.Join(ti.State, "runs")
	runs, _ := os.ReadDir(ws)
	for _, r := range runs {
		stopLeftover(t, filepath.Join(ws, r.Name(), "cluster"))
	}
}
