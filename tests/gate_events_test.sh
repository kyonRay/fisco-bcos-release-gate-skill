#!/usr/bin/env bash
# gate.sh must report what it judged over fd 3 -- scenario_finished {name, result} and
# oracle_check {oracle, phase, verdict} -- because the host's gate document and `fbt report` are
# built from those events and nothing else. The first real 3.18.0 round passed three scenario
# families and left a transcript with six host events and no stage: the engine had never emitted
# them. The shapes are checked by emitting through the real event_lib with fd 3 captured; the call
# sites are pinned statically (a real run is the only thing that reaches them).
set -euo pipefail
cd "$(dirname "$0")/.."
source tests/assert.sh

emit() { bash -c 'source scripts/event_lib.sh; emit_event "$@"' _ "$@" 3>&1 1>/dev/null; }

line="$(emit scenario_finished name malformed result pass)"
assert_contains "$line" '"ev":"scenario_finished"' "scenario_finished event name"
assert_contains "$line" '"name":"malformed"' "scenario_finished carries the family"
assert_contains "$line" '"result":"pass"' "scenario_finished carries the result"

line="$(emit oracle_check oracle state-mismatch phase "after:dual_rpc" verdict trip)"
assert_contains "$line" '"oracle":"state-mismatch"' "oracle_check carries the oracle"
assert_contains "$line" '"phase":"after:dual_rpc"' "oracle_check carries the phase"
assert_contains "$line" '"verdict":"trip"' "oracle_check carries the verdict"

# Call sites: one started + two finished (pass/fail) per scenario loop, and every oracle outcome.
assert_eq "1" "$(grep -c 'emit_event scenario_started name "\$name"' scripts/gate.sh)" "scenario_started emitted in the loop"
assert_eq "1" "$(grep -c 'emit_event scenario_finished name "\$name" result pass' scripts/gate.sh)" "scenario pass emitted"
assert_eq "1" "$(grep -c 'emit_event scenario_finished name "\$name" result fail' scripts/gate.sh)" "scenario fail emitted"
for o in crash halt state-mismatch; do
    assert_eq "1" "$(grep -c "emit_event oracle_check oracle $o phase \"\$phase\" verdict pass" scripts/gate.sh)" "$o pass emitted"
    assert_eq "1" "$(grep -c "emit_event oracle_check oracle $o phase \"\$phase\" verdict trip" scripts/gate.sh)" "$o trip emitted"
done
assert_eq "2" "$(grep -c 'emit_event oracle_check oracle state-mismatch phase "\$phase" verdict error' scripts/gate.sh)" "stateroot's two cannot-judge paths emit error"
# The upgrade timeline reports the same way: one scenario_finished for the stage, oracle_check per
# check in its T1/T7/T8 triads.
assert_eq "1" "$(grep -c 'emit_event scenario_finished name upgrade result pass' scripts/gate_upgrade.sh)" "upgrade pass emitted"
assert_eq "1" "$(grep -c 'emit_event scenario_finished name upgrade result fail' scripts/gate_upgrade.sh)" "upgrade fail emitted"
assert_eq "3" "$(grep -c 'emit_event oracle_check oracle [a-z-]* phase "\$phase" verdict pass' scripts/scenarios/scenario_upgrade.sh)" "the triad emits a pass per oracle"
assert_eq "3" "$(grep -c 'emit_event oracle_check oracle [a-z-]* phase "\$phase" verdict trip' scripts/scenarios/scenario_upgrade.sh)" "the triad emits a trip per oracle"
assert_done
