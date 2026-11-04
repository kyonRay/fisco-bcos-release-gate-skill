package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kyonRay/fisco-bcos-release-gate-skill/internal/exitcode"
)

// gate upgrade builds a chain on the configured band and used to leave it running on purpose. It
// reserves the ports like a gate round, and now cleans up like one unless told to keep the chain.
func upgradeStub(outcome string) string {
	return `source "$(dirname "$0")/event_lib.sh"
event_begin_command gate_upgrade.sh
ws=""; while [ $# -gt 0 ]; do [ "$1" = -o ] && ws="$2"; shift; done
mkdir -p "$ws/127.0.0.1/node0"
printf '#!/bin/sh\ntrap '"'"'kill $child'"'"' TERM INT\nsleep 300 & child=$!\nwait $child\n' > "$ws/127.0.0.1/node0/fisco-bcos"
printf '#!/bin/sh\npkill -f "$(cd "$(dirname "$0")" && pwd)/node0/fisco-bcos"; true\n' > "$ws/127.0.0.1/stop_all.sh"
chmod +x "$ws/127.0.0.1/node0/fisco-bcos" "$ws/127.0.0.1/stop_all.sh"
( perl -MPOSIX -e 'POSIX::setsid(); exec @ARGV' sh "$ws/127.0.0.1/node0/fisco-bcos" >/dev/null 2>&1 & )
sleep 1
event_set_outcome ` + outcome + `
`
}

func TestGateUpgradeStopsItsChainAndKeepsItOnlyWhenAsked(t *testing.T) {
	needPerl(t)
	for _, tc := range []struct {
		name      string
		args      []string
		wantNodes int
	}{
		{"default", nil, 0},
		{"keep", []string{"--keep-cluster"}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ti, flags := gateFixture(t, cleanGate, cleanCase)
			ti.WriteScript(t, "gate_upgrade.sh", upgradeStub("pass"))
			bin := filepath.Join(ti.Root, "repo", "fisco-bcos")
			args := append(append(flags, "gate", "upgrade", "-p", "default-latest",
				"--old-bin", bin, "--new-bin", bin, "--target-ver", "3.18.0"), tc.args...)
			code, _, errOut := runReal(t, args...)
			runs, err := os.ReadDir(filepath.Join(ti.State, "runs"))
			if err != nil || len(runs) != 1 {
				t.Fatalf("runs = %v, %v", runs, err)
			}
			ws := filepath.Join(ti.State, "runs", runs[0].Name(), "cluster")
			stopLeftover(t, ws)
			if code != exitcode.OK {
				t.Fatalf("code = %v (%s)", code, errOut)
			}
			if got := nodesUnder(ws); got != tc.wantNodes {
				t.Errorf("nodes running = %d, want %d", got, tc.wantNodes)
			}
			if ids := registered(t, ti); len(ids) != tc.wantNodes {
				t.Errorf("registry = %v, want %d entries", ids, tc.wantNodes)
			}
			_, statErr := os.Stat(ws)
			if (tc.wantNodes == 0) != os.IsNotExist(statErr) {
				t.Errorf("cluster directory stat = %v with %d nodes wanted", statErr, tc.wantNodes)
			}
		})
	}
}
