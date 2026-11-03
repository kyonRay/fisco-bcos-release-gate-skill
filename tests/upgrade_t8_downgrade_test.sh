#!/usr/bin/env bash
# T8 asserts that the chain REFUSES to lower compatibility_version, and the verdict is pure:
# _upg_downgrade_refused(console_out, readback, target). Before this, T8 "rolled back" by setting
# the version down with the console's output discarded -- a step the chain rejects every time, and
# which therefore reported success without ever succeeding. The verdict needs both the unchanged
# readback AND the chain's own rule in the console output: a version left unchanged for any other
# reason (pending proposal, client-side rejection, permissions) is not a tested refusal.
set -euo pipefail
cd "$(dirname "$0")/.."
source tests/assert.sh
source scripts/scenarios/scenario_upgrade.sh

rule='{
  "mode": "direct",
  "ok": false,
  "status": 16,
  "message": "Set compatibility version should not lower than version nowadays."
}'
rc=0; _upg_downgrade_refused "$rule" 3.18.0 3.18.0 || rc=$?
assert_eq 0 "$rc" "direct path: rule message + version unchanged -> refused (0)"

# What a 3.18.0 chain really answers (checkValueValid re-wraps the lowering error): seen live.
wrapped='{
  "message" : "The value for compatibility_version must be a valid number.",
  "method" : "createSetSysConfigProposal(string,string,uint256)",
  "mode" : "proposal",
  "ok" : false,
  "status" : 16
}'
rc=0; _upg_downgrade_refused "$wrapped" 3.18.0 3.18.0 || rc=$?
assert_eq 0 "$rc" "the precompile's re-wrapped wording of the version rule counts as the refusal (0)"

prop='{
  "mode": "proposal",
  "ok": false,
  "proposal": {
    "id": 3,
    "statusName": "executed",
    "execCode": 16
  }
}'
rc=0; _upg_downgrade_refused "$prop" 3.18.0 3.18.0 || rc=$?
assert_eq 0 "$rc" "committee path: proposal executed with a non-zero execCode + version unchanged -> refused (0)"

rc=0; _upg_downgrade_refused "$rule" 3.16.4 3.18.0 || rc=$?
assert_eq 1 "$rc" "version moved down -> NOT refused (1), whatever the console said"

rc=0; _upg_downgrade_refused '{"code":0,"msg":"Success"}' 3.18.0 3.18.0 || rc=$?
assert_eq 1 "$rc" "console claims success but the value did not move -> not a refusal we understand (1)"

rc=0; _upg_downgrade_refused '{"code":-50000,"msg":"Permission denied"}' 3.18.0 3.18.0 || rc=$?
assert_eq 1 "$rc" "refused for permissions, not for the version rule -> (1)"

pending='{
  "mode": "proposal",
  "ok": true,
  "proposal": {
    "id": 4,
    "statusName": "voting",
    "execCode": 0
  }
}'
rc=0; _upg_downgrade_refused "$pending" 3.18.0 3.18.0 || rc=$?
assert_eq 1 "$rc" "proposal still voting -> the rule was never exercised (1)"

rc=0; _upg_downgrade_refused "" "" 3.18.0 || rc=$?
assert_eq 1 "$rc" "empty readback and empty output is never a refusal (1)"

out="$(SCENARIO_DRY=1 scenario_upgrade_run /tmp/x /tmp/old-bin /tmp/new-bin 3.17.0)"
assert_contains "$out" "T8 optional downgrade refusal" "dry plan describes T8 as a refusal, not a rollback"
assert_not_contains "$out" "swap binaries back" "dry plan no longer promises a binary swap-back"
assert_done
