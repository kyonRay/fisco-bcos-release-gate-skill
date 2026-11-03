#!/usr/bin/env bash
# apply_profile.sh — replay a captured production .profile onto a local FISCO-BCOS AIR cluster.
#
# Usage:
#   apply_profile.sh -p <profile> [-o outdir] [--dry-run] [-h]
#     -p  path to a .profile file (required)          e.g. profiles/production-enterprise.profile
#     -o  output dir for the generated cluster         (default ./nodes-release-gate)
#     --dry-run  print the plan to stdout and exit — no build_chain, no cluster_up.sh, no network.
#                Prints: (1) the build_chain invocation incl. compatibility_version, (2) the
#                config.ini patch lines from [config_ini_override], (3) one `sysconfig set`
#                line per [system_config_replay] pair. Flags absent from the profile are never
#                invented or emitted.
#     -h  print this help and exit
#
#   Host-provided env vars (not part of any .profile file):
#     FISCO_BIN   specific fisco-bcos binary for cluster_up.sh -e (default: cluster_up.sh's own)
#     WEB3_BASE   Web3 RPC base port for cluster_up.sh -w; OUTRANKS the profile's own captured
#                 web3_rpc.listen_port in the later config.ini patch too (default: 8545)
#     OPS_BIN     the fisco-bcos binary whose subcommands (sysconfig / balance, bcos-ops) replay
#                 the profile in step 4 (default: FISCO_BIN). Only the upgrade timeline sets it
#                 separately: its T0 chain runs an old binary that has no subcommands.
#
# Real-run (no --dry-run; needs a live fisco-bcos binary — NOT exercised by this skill's own
# tests, since there is no binary in this environment):
#   1. cluster_up.sh (sibling fisco-bcos-testing skill) runs build_chain + start_all with this
#      profile's compatibility_version + cluster topology (-v/-n/-p/-s, plus -e/-w from the env
#      vars above), and waits for RPC to answer.
#   2. Each node's config.ini is patched per [config_ini_override] (web3_rpc.listen_port keeps
#      whatever WEB3_BASE-derived port step 1 already set; every other listen_port key gets the
#      profile's own base+i renumbering).
#   3. The cluster is restarted so the config.ini patch takes effect.
#   4. Each [system_config_replay] pair is written with `fisco-bcos sysconfig set` (signing as the
#      genesis admin key build_chain wrote to <outdir>/ca/accounts) and READ BACK with
#      `sysconfig get`; a value that does not read back as written fails the bring-up.
set -euo pipefail

# Requires bash 4+ (profile_lib.sh uses associative arrays). Fail clearly instead of a cryptic
# `declare: -gA: invalid option` on stock macOS bash 3.2.
if (( BASH_VERSINFO[0] < 4 )); then
    echo "apply_profile.sh requires bash >= 4 (found ${BASH_VERSION}). On macOS: brew install bash." >&2
    exit 1
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# fd 3 event protocol (design doc §8). Sourced and armed BEFORE any flag parsing: a usage error
# that exits two lines from now must still produce a command_finished, or the host sees a
# subprocess that died without terminating and reports its own bug (40) for what is really a
# config error (20). Every call is a no-op when fd 3 is not open, so standalone runs are unchanged.
source "$SCRIPT_DIR/event_lib.sh"
event_begin_command apply_profile.sh
# _SELF_DIR is the resolver's "own dir" candidate — overridable so tests/relocatable_test.sh can
# eval just the _resolve_engine_script function body (below) against a fixture dir without
# sourcing this whole script.
_SELF_DIR="${_SELF_DIR:-$SCRIPT_DIR}"

PROFILE_PATH=""
OUTDIR="./nodes-release-gate"
DRY_RUN=0

# Pull the long --dry-run flag out before getopts sees the rest (getopts only knows short opts).
args=()
for arg in "$@"; do
    case "$arg" in
        --dry-run) DRY_RUN=1 ;;
        *) args+=("$arg") ;;
    esac
done
set -- "${args[@]+"${args[@]}"}"

while getopts "p:o:h" opt; do
    case "$opt" in
        p) PROFILE_PATH="$OPTARG" ;;
        o) OUTDIR="$OPTARG" ;;
        h) grep '^#' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
        *) echo "bad flag; -h for help" >&2; exit 2 ;;
    esac
done

[[ -z "$PROFILE_PATH" ]] && { echo "ERROR: -p <profile> is required. -h for help." >&2; exit 2; }
# exit 2 = config_error under the event protocol; exit 1 would be reported as an fbt
# bug (40) for what is a mistyped or missing -p.
[[ -f "$PROFILE_PATH" ]] || { echo "ERROR: profile not found: $PROFILE_PATH" >&2; exit 2; }

# _apply_profile_cluster_up_args <outdir> <version> <fisco_bin> <node_count> <ports> <web3_base>
# <sm_mode> — fills global APPLY_CU_ARGS with the cluster_up.sh argv (Task 3's cluster_up.sh
# -v/-w passthrough) needed to bring up this profile's topology. Empty fisco_bin/web3_base omit
# -e/-w so cluster_up.sh falls back to its own defaults (repo-root binary / port 8545); sm_mode
# "1" adds the bare -s flag.
#
# `return 0` is required, not decorative — the same trap Task 3's own
# _cluster_up_build_chain_argv hit: if this function's LAST executed statement were the trailing
# `[[ "$sm_mode" == "1" ]] && ...` and sm_mode were "0" (the common non-SM case), that conditional
# evaluates false and the function itself returns 1 under set -euo pipefail — aborting the whole
# script at its bare top-level call site below.
_apply_profile_cluster_up_args() {
    local outdir="$1" version="$2" fisco_bin="$3" node_count="$4" ports="$5" web3_base="$6" sm_mode="$7"
    APPLY_CU_ARGS=(-o "$outdir" -v "$version" -n "$node_count" -p "$ports")
    [[ -n "$fisco_bin" ]] && APPLY_CU_ARGS+=(-e "$fisco_bin")
    [[ -n "$web3_base" ]] && APPLY_CU_ARGS+=(-w "$web3_base")
    [[ "$sm_mode" == "1" ]] && APPLY_CU_ARGS+=(-s)
    return 0
}

# _resolve_engine_script <name> — abs path to a sibling fisco-bcos-testing engine script (e.g.
# cluster_up.sh), tried in order: $FBT_ENGINE_SCRIPTS (an installed libexec layout with no sibling
# checkout points this at wherever the engine scripts actually live) -> this script's own dir
# ($_SELF_DIR) -> the dev-checkout sibling ../../fisco-bcos-testing/scripts. Returns 1 (nothing on
# stdout) if none of the three candidates has the file — the caller decides how to fail.
_resolve_engine_script() {
    local n="$1" c
    for c in "${FBT_ENGINE_SCRIPTS:-}" "$_SELF_DIR" "$_SELF_DIR/../../fisco-bcos-testing/scripts"; do
        [[ -n "$c" && -f "$c/$n" ]] && { echo "$c/$n"; return 0; }
    done
    return 1
}

# _apply_profile_web3_port <profile_listen_port> <node_index> <web3_base_override> — the
# web3_rpc.listen_port value to write into node<node_index>'s config.ini. A host-provided
# web3_base_override (from $WEB3_BASE) OUTRANKS the profile's own captured port: without this, the
# config.ini patch loop's later pass over [config_ini_override] would blindly rewrite the port
# cluster_up.sh already brought the cluster up on (base+i, per WEB3_BASE) back to the profile's own
# captured base+i — silently undoing the override. Empty override falls back to the profile's own
# port.
_apply_profile_web3_port() {
    local profile_port="$1" node_idx="$2" override="$3" base="$1"
    [[ -n "$override" ]] && base="$override"
    echo $((base + node_idx))
}

source "$SCRIPT_DIR/profile_lib.sh"
profile_load "$PROFILE_PATH"

genesis_compat="${PROFILE_GENESIS[compatibility_version]:-}"
[[ -z "$genesis_compat" ]] && { echo "ERROR: profile has no [genesis] compatibility_version" >&2; exit 1; }

# Cluster topology for the cluster_up.sh call below (both dry-run and real-run paths share it, so
# the dry-run plan text matches what a real run would actually do). node_count comes from the
# profile's own [meta] node_count (e.g. rpbft-scale.profile's 7), default 4 — matching
# cluster_up.sh's own default. sm_mode comes from the profile's [genesis] sm_crypto. FISCO_BIN and
# WEB3_BASE are host-provided env vars (not part of any profile file): FISCO_BIN selects a specific
# fisco-bcos binary, WEB3_BASE overrides the Web3 RPC base port cluster_up.sh brings the cluster up
# on (and, via _apply_profile_web3_port below, the port the later config.ini patch loop keeps).
node_count="${PROFILE_META[node_count]:-4}"
sm_mode=0
[[ "${PROFILE_GENESIS[sm_crypto]:-}" == "true" ]] && sm_mode=1
# The p2p and BCOS-RPC bases are host-overridable for the same reason WEB3_BASE is: a second
# cluster on one machine has to listen somewhere else, and a machine that already runs a chain on
# build_chain's defaults has none of them free. Hardcoding them made every port setting a no-op --
# the host reserved 31300/21200, the chain still bound 30300/20200, and the nodes died on "Address
# already in use" after a full build_chain.
cu_ports="${P2P_BASE_PORT:-30300},${BCOS_RPC_BASE_PORT:-20200}"
_apply_profile_cluster_up_args "$OUTDIR" "$genesis_compat" "${FISCO_BIN:-}" "$node_count" "$cu_ports" "${WEB3_BASE:-}" "$sm_mode"

if [[ "$DRY_RUN" == 1 ]]; then
    echo "== apply_profile dry-run =="
    echo "profile: $PROFILE_PATH"
    echo "outdir:  $OUTDIR"
    echo ""
    echo "[1/3] cluster_up.sh (wraps build_chain.sh + start_all.sh):"
    echo "  cluster_up.sh ${APPLY_CU_ARGS[*]}   # compatibility_version $genesis_compat"
    echo ""
    echo "[2/3] config.ini patch (from [config_ini_override]):"
    while read -r pair; do
        [[ -z "$pair" ]] && continue
        fullkey="${pair%% *}"
        value="${pair#* }"
        if [[ "$fullkey" == *listen_port && "$value" =~ ^[0-9]+$ ]]; then
            echo "  ${fullkey} = ${value}+i   # per-node: node<i> gets $value+i (see the patch loop)"
        else
            echo "  ${fullkey} = ${value}"
        fi
    done < <(profile_config_pairs)
    echo ""
    echo "[3/3] sysconfig replay (fisco-bcos sysconfig set, then sysconfig get read-back; from [system_config_replay]):"
    while read -r pair; do
        [[ -z "$pair" ]] && continue
        key="${pair%% *}"
        value="${pair#* }"
        echo "  sysconfig set ${key} ${value}"
    done < <(profile_replay_pairs)
    exit 0
fi

# ---------------------------------------------------------------------------
# Real-run — needs live chain. Never reached from --dry-run.
# ---------------------------------------------------------------------------

# CLUSTER_UP is overridable via env for testing (a spy/recorder script stands in for the real
# cluster_up.sh so a test can assert the real call's argv without a live chain). When not
# overridden, resolve it via _resolve_engine_script so an installed libexec layout (no sibling
# fisco-bcos-testing checkout) can point FBT_ENGINE_SCRIPTS at wherever the engine scripts live.
if [[ -z "${CLUSTER_UP:-}" ]]; then
    CLUSTER_UP="$(_resolve_engine_script cluster_up.sh)" || {
        event_set_outcome infra_error   # the sibling skill is not installed: fix the machine (30)
        echo "ERROR: cluster_up.sh not found (tried \$FBT_ENGINE_SCRIPTS, $_SELF_DIR, sibling fisco-bcos-testing/scripts — expected the fisco-bcos-testing skill checked out alongside this one, or FBT_ENGINE_SCRIPTS set)" >&2
        exit 1
    }
fi
[[ -f "$CLUSTER_UP" ]] || {
    echo "ERROR: sibling skill script not found: $CLUSTER_UP (expected the fisco-bcos-testing skill checked out alongside this one)" >&2
    exit 1
}

echo ">> [1/4] cluster_up (needs live chain): build_chain + start_all into $OUTDIR"
# Threads this profile's compatibility_version + cluster topology into cluster_up.sh (Task 3's
# -v/-n/-p/-w/-s/-e), via the SAME APPLY_CU_ARGS array the dry-run plan above already printed —
# the "GAP: no compatibility_version passthrough" limitation this comment used to describe here is
# now closed.
bash "$CLUSTER_UP" "${APPLY_CU_ARGS[@]}"
# Absolute from here on: NODE_DIR is what every subcommand and scenario is pointed at.
OUTDIR="$(cd "$OUTDIR" && pwd)"
NODE_DIR="$OUTDIR/127.0.0.1"

echo ">> [2/4] patching config.ini per profile (needs live chain)"
while read -r pair; do
    [[ -z "$pair" ]] && continue
    fullkey="${pair%% *}"
    value="${pair#* }"
    section="${fullkey%%.*}"
    key="${fullkey#*.}"
    for cfg in "$NODE_DIR"/node*/config.ini; do
        [[ -f "$cfg" ]] || continue
        # A captured production profile legitimately holds ONE listen_port per service, because
        # production runs one node per host. Writing that single port into all 4 local nodes makes
        # every node but the first die with "acceptor bind failed" — and since only node0 is
        # probed for readiness, the cluster then looks healthy while consensus can never reach
        # quorum. Renumber port values base+index, the convention build_chain already uses for the
        # p2p and rpc ports.
        node_value="$value"
        if [[ "$key" == *listen_port && "$value" =~ ^[0-9]+$ ]]; then
            node_idx="$(basename "$(dirname "$cfg")")"
            node_idx="${node_idx#node}"
            if [[ "$section" == "web3_rpc" && "$key" == "listen_port" ]]; then
                # WEB3_BASE (if the host set it) outranks the profile's own captured port here —
                # cluster_up.sh above already brought this cluster up on WEB3_BASE+i (Task 3's -w),
                # so blindly applying the profile's base+i would silently undo that override.
                node_value="$(_apply_profile_web3_port "$value" "$node_idx" "${WEB3_BASE:-}")"
            else
                node_value=$((value + node_idx))
            fi
        fi
        # Pass section/key/value via env instead of interpolating them into the perl source
        # text (a value containing '/', '$', '@', or a quote would otherwise corrupt the
        # source or prematurely close the s/// delimiter). {} delimiters sidestep the '/'
        # that can legitimately appear in a config value (e.g. a path).
        # A substitution that matches nothing is the quiet killer here: the key gets silently
        # skipped, the node keeps its generated default, and the run still claims to reproduce
        # production. Make perl exit 9 when it changed nothing so a profile naming a key this
        # build does not emit fails loudly instead.
        patch_rc=0
        SECTION="$section" KEY="$key" VALUE="$node_value" perl -0pi -e '
            $n += s{(\[\Q$ENV{SECTION}\E\][^\[]*?\n\s*\Q$ENV{KEY}\E\s*=\s*)\S+}{$1$ENV{VALUE}}s;
            END { exit($n ? 0 : 9) }
        ' "$cfg" || patch_rc=$?
        if [[ "$patch_rc" == 9 ]]; then
            echo "ERROR: apply_profile: [$section] $key not found in $cfg — nothing was patched, so the profile is NOT applied." >&2
            echo "       The profile names a config key this build does not emit (check the [$section] block in the generated config.ini and NodeConfig.cpp for the current key name)." >&2
            exit 1
        elif [[ "$patch_rc" != 0 ]]; then
            echo "ERROR: apply_profile: patching [$section] $key in $cfg failed (perl exit $patch_rc)" >&2
            exit 1
        fi
    done
done < <(profile_config_pairs)

echo ">> [3/4] restarting cluster to pick up config.ini patch (needs live chain)"
bash "$NODE_DIR/stop_all.sh"
bash "$NODE_DIR/start_all.sh"

source "$SCRIPT_DIR/ops_lib.sh"

# _fund_accounts — give the admin (it signs every write below) and every address the caller's
# scenarios spend from a balance, so transactions can pay for gas once tx_gas_price is non-zero.
# Mirrors tools/.ci/ci_check_air.sh's own sequence (feature_balance -> feature_balance_precompiled
# -> balance add, signed with the governor pem). Called from the replay loop the moment
# feature_balance_precompiled is enabled, which the profile's own key order guarantees is before
# tx_gas_price is set.
_fund_accounts() {
    # tools/.ci/java_sdk_demo_ci_test.sh funds 1e10, but that CI chain never sets tx_gas_price, so
    # gas is free there. Under the production profile (tx_gas_price 0x5208) a single 500000-gas
    # deploy costs 1.05e10 -- just over CI's figure, and the transaction is rejected with
    # {"code":-32603,"message":"InsufficientFunds"}. Fund with headroom instead of the CI number.
    local amount="${RG_FUND_AMOUNT:-1000000000000000000}"
    local pem gov target out
    pem="$(_ops_admin_pem)" || return 1
    gov="$(basename "$pem" .pem)"
    # The governor first (it signs everything the replay itself sends), then every extra account the
    # caller says its scenarios will spend from. RG_FUND_ADDRESSES is explicit on purpose: the Web3
    # leg signs with its own key, and an unfunded account there surfaces as
    # {"code":-32603,"message":"InsufficientFunds"} from eth_sendRawTransaction. Listing the
    # addresses beats probing the chain to guess whether balance accounting is on.
    for target in "$gov" ${RG_FUND_ADDRESSES:-}; do
        target="${target//,/}"
        [[ -n "$target" ]] || continue
        echo "  balance add $target $amount"
        out="$(_ops_write balance add "$target" "$amount" 2>&1)" || true
        if ! _ops_ok "$out"; then
            echo "ERROR: apply_profile: balance add $target $amount did not report ok — transactions from that account will fail for lack of gas money. The binary said:" >&2
            sed 's/^/         /' <<<"$out" >&2
            return 1
        fi
    done
}

echo ">> [4/4] replaying [system_config_replay] with fisco-bcos sysconfig set (needs live chain)"
# start_all.sh returns as soon as the processes exist; the RPC port opens a few seconds later. The
# first run on 3.18.0 sent its first write into "Connection refused" (the console's JVM start-up
# used to hide this gap).
_ops_wait_ready 60 || exit 1
# Each pair is written, then READ BACK and compared. `ok` alone is not the verdict: on a governance
# chain the write becomes a committee proposal, and the preset reports ok while the proposal is
# still voting (bcos-ops PresetCall.cpp) -- the value on chain is the only thing that proves the
# profile applied. Without this a run where the production flags silently failed to apply still
# reports "cluster ready", and the gate then tests a DEFAULT-config chain while claiming
# production fidelity, which is the one false-green this whole script exists to prevent.
while read -r pair; do
    [[ -z "$pair" ]] && continue
    key="${pair%% *}"
    value="${pair#* }"
    echo "  sysconfig set $key $value"
    set_out="$(_ops_write sysconfig set "$key" "$value" 2>&1)" || true
    if ! _ops_ok "$set_out"; then
        echo "ERROR: apply_profile: sysconfig set $key $value did not report ok — the profile is NOT applied, refusing to report this cluster as production-faithful. The binary said:" >&2
        sed 's/^/         /' <<<"$set_out" >&2
        exit 1
    fi
    get_out="$(_ops_read sysconfig get "$key" 2>&1)" || true
    readback="$(_ops_json_top "$get_out" value)"
    if [[ "$readback" != "$value" ]]; then
        echo "ERROR: apply_profile: $key reads back as '$readback' after setting '$value' — the profile is NOT applied as written (a proposal still voting, or the chain stores the value differently). The binary said:" >&2
        sed 's/^/         /' <<<"$get_out" >&2
        exit 1
    fi
    # Fund immediately after the balance precompiled goes live: from here on the chain charges gas
    # to a real balance, and the profile's key order puts tx_gas_price later (see the ordering note
    # in the profile's [system_config_replay] section).
    if [[ "$key" == "feature_balance_precompiled" && "$value" != "0" ]]; then
        _fund_accounts || exit 1
    fi
done < <(profile_replay_pairs)

echo ">> cluster ready at $OUTDIR (profile: $PROFILE_PATH)"
