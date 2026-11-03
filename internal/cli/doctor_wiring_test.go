package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kyonRay/fisco-bcos-release-gate-skill/internal/exitcode"
	"github.com/kyonRay/fisco-bcos-release-gate-skill/internal/testinstall"
)

// case run and gate upgrade both bring a chain up through apply_profile.sh, so a binary without the
// ops subcommands is refused before T0 -- not discovered as a "gate failure" four node starts later.
func TestCaseRunAndGateUpgradeCheckTheirDependenciesFirst(t *testing.T) {
	ti := testinstall.New(t)
	stubEngine(t, ti, cleanGate, cleanCase)
	repo := filepath.Join(ti.Root, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(repo, "fisco-bcos")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	ossl := filepath.Join(repo, "openssl")
	if err := os.WriteFile(ossl, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A binary that exists but answers `help` with nothing: present as a path, no subcommands.
	// That is the machine's problem (a 3.17 build), class infra, exit 30.
	cfg := ti.WriteConfig(t, "repo:\n  root: "+repo+"\ntools:\n  fisco_bin: "+bin+"\n  openssl_cmd: "+ossl+"\n"+
		"cluster:\n  p2p_base_port: 45300\n  bcos_base_port: 45200\n  web3_base_port: 45545\n")
	flags := []string{"--engine-dir", ti.Root, "--state-dir", ti.State, "--config", cfg}
	writeCase(t, ti, "live.case", "active")

	code, _, errOut := runReal(t, append(flags, "case", "run", "live.case")...)
	if code != exitcode.Infra || !strings.Contains(errOut, "subcommands") {
		t.Errorf("case run: code = %v, err = %q; want 30 naming the missing subcommands", code, errOut)
	}
	code, _, errOut = runReal(t, append(flags, "gate", "upgrade", "-p", "default-latest",
		"--old-bin", bin, "--new-bin", bin, "--target-ver", "3.18.0")...)
	if code != exitcode.Infra || !strings.Contains(errOut, "subcommands") {
		t.Errorf("gate upgrade: code = %v, err = %q; want 30 naming the missing subcommands", code, errOut)
	}
}
