package config

import "testing"

// The engine scripts test their booleans with `== 1`. fbt.yaml's `upgrade.rollback: true` used to
// reach the engine as UPGRADE_ROLLBACK=true, and T8 silently skipped.
func TestEngineBooleansArriveAsOneOrZero(t *testing.T) {
	r := Resolved{
		"upgrade.rollback": Value{S: "true"},
		"fuzz.continue":    Value{S: "false"},
		"fuzz.seed":        Value{S: "42"},
	}
	got := r.EngineValues()
	for k, want := range map[string]string{"upgrade.rollback": "1", "fuzz.continue": "0", "fuzz.seed": "42"} {
		if got[k] != want {
			t.Errorf("%s = %q, want %q", k, got[k], want)
		}
	}
	if (Resolved{"upgrade.rollback": Value{S: "1"}}).EngineValues()["upgrade.rollback"] != "1" {
		t.Error("a 1 that is already an engine literal must stay 1")
	}
}
