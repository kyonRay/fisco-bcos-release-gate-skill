#!/usr/bin/env bash
# T8 asserts that the chain REFUSES to lower compatibility_version, and the verdict is pure:
# _upg_downgrade_refused(readback, target, orig). Before this, T8 "rolled back" by setting the
# version down with the console's output discarded -- a step the chain rejects every time, and
# which therefore reported success without ever succeeding.
set -euo pipefail
cd "$(dirname "$0")/.."
source tests/assert.sh
source scripts/scenarios/scenario_upgrade.sh

rc=0; _upg_downgrade_refused 3.18.0 3.18.0 3.16.4 || rc=$?
assert_eq 0 "$rc" "version read back unchanged at target -> refused (0)"

rc=0; _upg_downgrade_refused 3.16.4 3.18.0 3.16.4 || rc=$?
assert_eq 1 "$rc" "version read back at the old value -> NOT refused (1)"

rc=0; _upg_downgrade_refused "" 3.18.0 3.16.4 || rc=$?
assert_eq 1 "$rc" "empty readback is never a refusal (1): an unanswered console is not evidence"

rc=0; _upg_downgrade_refused 3.18.0 3.18.0 3.18.0 || rc=$?
assert_eq 2 "$rc" "genesis already at target -> nothing to refuse (2), caller reports SKIP"

out="$(SCENARIO_DRY=1 scenario_upgrade_run /tmp/x /tmp/old-bin /tmp/new-bin 3.17.0)"
assert_contains "$out" "T8 optional downgrade refusal" "dry plan describes T8 as a refusal, not a rollback"
assert_not_contains "$out" "swap binaries back" "dry plan no longer promises a binary swap-back"
assert_done
