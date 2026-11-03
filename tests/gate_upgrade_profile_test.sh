#!/usr/bin/env bash
# gate_upgrade.sh must hand the -p profile to scenario_upgrade_run as its 5th argument. Before this
# test existed the argument was dropped, so every upgrade ran T0 from the production-enterprise
# default whatever -p said, and profiles/upgrade-legacy.profile was never used by anything.
set -euo pipefail
cd "$(dirname "$0")/.."
source tests/assert.sh

PROFILE=profiles/upgrade-legacy.profile
out="$(bash scripts/gate_upgrade.sh -p "$PROFILE" --old-bin "$PROFILE" --new-bin "$PROFILE" \
        --target-ver 3.17.0 --dry-run 2>&1)"
call_line="$(grep 'would call scenario_upgrade_run' <<<"$out")"
assert_contains "$call_line" "/profiles/upgrade-legacy.profile" \
    "dry-run names the -p profile as the 5th scenario_upgrade_run argument (absolute path)"
assert_not_contains "$call_line" "production-enterprise" \
    "dry-run does not fall back to the production-enterprise default"
assert_contains "$(grep -c 'scenario_upgrade_run "\$OUTDIR_ABS" "\$OLD_BIN" "\$NEW_BIN" "\$TARGET_VER" "\$PROFILE_ABS"' scripts/gate_upgrade.sh)" "1" \
    "the real-run call site passes PROFILE_ABS too (the dry line is not the only place)"

# The 3.18.0 roadmap entry resolves: the T6 flag list for the current release is derived from
# Features.cpp, never hardcoded, so this only checks that the derivation still works for it.
source scripts/scenarios/scenario_upgrade.sh
features_cpp="$(_upg_find_repo_root 2>/dev/null)/bcos-framework/bcos-framework/ledger/Features.cpp"
if [[ -f "$features_cpp" ]]; then
    flags="$(_upg_target_flags 3.18.0 "$features_cpp")"
    assert_contains "$flags" "bugfix_eip161_1052_account_semantics" \
        "_upg_target_flags(3.18.0) reads the V3_18_0_VERSION roadmap entry"
else
    echo "SKIP: Features.cpp not found (not inside a FISCO-BCOS checkout)"
fi
assert_done
