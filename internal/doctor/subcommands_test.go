package doctor

import (
	"strings"
	"testing"
)

// The engine drives the chain through the binary's own subcommands, so a binary without them (3.17
// and earlier) must be refused before a chain is built, not discovered inside apply_profile.sh.
func TestABinaryWithoutSubcommandsIsRefusedAsInfra(t *testing.T) {
	plan := []Requirement{{Name: "subcommands", Kind: KindSubcommands, ConfigKey: "tools.ops_bin", Why: "w"}}
	p := everything()
	p.files["/old/fisco-bcos"] = true // present, but a 3.17 binary

	rep, err := CheckIn("/repo", plan, map[string]string{"tools.fisco_bin": "/old/fisco-bcos"}, p)
	if err == nil || rep.Deps[0].Class != "infra" || !strings.Contains(rep.Deps[0].Detail, "no ops subcommands") {
		t.Errorf("a 3.17 binary was accepted: %+v (%v)", rep.Deps[0], err)
	}
	// tools.ops_bin is optional: the binary under test is what gets checked when it is unset...
	p.subcmd["/old/fisco-bcos"] = true
	if _, err := CheckIn("/repo", plan, map[string]string{"tools.fisco_bin": "/old/fisco-bcos"}, p); err != nil {
		t.Errorf("fisco_bin with subcommands was refused: %v", err)
	}
	// ...and when it is set, it is the one that must carry them (the upgrade timeline's new_bin).
	p.files["/new/fisco-bcos"] = true
	cfg := map[string]string{"tools.fisco_bin": "/old/fisco-bcos", "tools.ops_bin": "/new/fisco-bcos"}
	if _, err := CheckIn("/repo", plan, cfg, p); err == nil {
		t.Error("an ops_bin without subcommands was accepted because fisco_bin had them")
	}
	p.subcmd["/new/fisco-bcos"] = true
	if _, err := CheckIn("/repo", plan, cfg, p); err != nil {
		t.Errorf("ops_bin with subcommands was refused: %v", err)
	}
	// Neither set: the user has to say which binary, a config error.
	rep, err = CheckIn("/repo", plan, map[string]string{}, p)
	if err == nil || rep.Deps[0].Class != "config" {
		t.Errorf("no binary at all became %+v", rep.Deps[0])
	}
}
