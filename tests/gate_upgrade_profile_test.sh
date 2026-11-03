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
# Guarded assignment: under set -e a bare `x="$(f)"` with f returning 1 kills the script before the
# SKIP branch, which is how this test used to pass only inside a FISCO checkout.
if repo_root="$(_upg_find_repo_root 2>/dev/null)" && [[ -f "$repo_root/bcos-framework/bcos-framework/ledger/Features.cpp" ]]; then
    features_cpp="$repo_root/bcos-framework/bcos-framework/ledger/Features.cpp"
    flags="$(_upg_target_flags 3.18.0 "$features_cpp")"
    assert_contains "$flags" "bugfix_eip161_1052_account_semantics" \
        "_upg_target_flags(3.18.0) reads the V3_18_0_VERSION roadmap entry"
else
    echo "SKIP: Features.cpp not found (not inside a FISCO-BCOS checkout)"
fi

# An installed layout has no FISCO tree above libexec; the host names the checkout through
# FBT_REPO_ROOT and the scenario must honour it (the first real upgrade run died on exactly this).
tmp="$(mktemp -d)"; trap 'rm -rf "$tmp"' EXIT
mkdir -p "$tmp/tools/BcosAirBuilder" && : > "$tmp/tools/BcosAirBuilder/build_chain.sh"
assert_eq "$tmp" "$(FBT_REPO_ROOT="$tmp" _upg_find_repo_root)" "_upg_find_repo_root honours FBT_REPO_ROOT"
rc=0; FBT_REPO_ROOT="$tmp/nonexistent" SCENARIO_UPG_DIR=/ _upg_find_repo_root >/dev/null || rc=$?
assert_eq 1 "$rc" "a FBT_REPO_ROOT without build_chain.sh is not taken on faith"
assert_eq "1" "$(grep -c 'elif (( rc == 2 )); then' scripts/gate_upgrade.sh)" "gate_upgrade.sh maps a cannot-run (2) to infra_error, not gate_fail"
assert_eq "0" "$(grep -c 'mkdir -p "\$OUTDIR"' scripts/gate_upgrade.sh)" "gate_upgrade.sh does not pre-create the -o directory build_chain will refuse"
assert_eq "1" "$(grep -c 'export NODE_DIR="\$node_dir_root"' scripts/scenarios/scenario_upgrade.sh)" "scenario_upgrade_run points NODE_DIR at its own -o workspace (PID discovery, ops, oracles all read it)"
assert_eq "1" "$(grep -c '^if scenario_upgrade_run ' scripts/gate_upgrade.sh)" "gate_upgrade.sh calls the timeline inside if, immune to errexit re-armed by sourced libraries"
assert_done
