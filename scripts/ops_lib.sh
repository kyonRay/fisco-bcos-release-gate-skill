#!/usr/bin/env bash
# ops_lib.sh — the engine's one way of talking to a chain through the node binary's own
# subcommands (`fisco-bcos sysconfig|balance|tx|status …`, bcos-ops, ADR 0009/0010). Sourced by
# apply_profile.sh, scenario_dual_rpc.sh, scenario_malformed.sh and scenario_upgrade.sh; it replaces
# every Java-console invocation those scripts used to issue.
#
# Why the subcommands and not the Java console: the console was a shared install with its own
# config.toml (which chain, which account), had to be invoked from its own directory, needed a JDK,
# and parsed values as Java integers. Every one of those was a real-chain failure in this project.
# The subcommands live in the binary under test, take the chain as `--node-dir`, the signer as
# `--account <pem>`, print JSON, and have an exit code contract (0 ok / 1 usage or connection /
# 2 the node refused or a check failed / 3 pending).
#
# This file sets no shell options at file scope: scenario_*.sh source it from inside their run
# functions, the same way they source oracle_lib.sh, and must not have their sourcing shell's
# options changed (see CLAUDE.md "Scripts").
#
# Env:
#   OPS_BIN    the binary whose subcommands are used (default: $FISCO_BIN, the binary under test).
#              Set separately only when the chain runs a DIFFERENT binary than the one that must
#              drive it — the upgrade timeline's T0/T1, where the chain is still on old_bin
#              (3.17 and earlier have no subcommands) and new_bin's subcommands reach it over
#              plain JSON-RPC (--node-dir falls back to the RPC port when no local socket exists).
#   NODE_DIR   cluster node-dir root, <outdir>/127.0.0.1 (default ./nodes-release-gate/127.0.0.1).
#              node0 is the node every command talks to; the admin key is <outdir>/ca/accounts.
#   OPS_TIMEOUT_MS  per-call timeout handed to --timeout (default 60000).
#
# Pure helpers (no IO, unit-tested in tests/ops_lib_test.sh): _ops_json_top, _ops_json_any,
# _ops_ok, _ops_smoke_step, _ops_smoke_contract, _ops_hex40_in.
# IO helpers (live chain only): _ops_bin, _ops_node0, _ops_admin_pem, _ops_read, _ops_write, _ops_tx.

# ---------------------------------------------------------------------------
# Pure: parse the pretty-printed JSON the subcommands write (jsoncpp styled: 2-space indent, one
# field per line, a space on BOTH sides of the colon -- `"ok" : true` -- keys sorted, arrays and
# objects opening on the next line). No jq — same convention as the rest of the engine. The
# patterns accept the colon with or without the surrounding spaces.
# ---------------------------------------------------------------------------

# _ops_json_top <json> <field> — the value of a TOP-LEVEL field (indented exactly two spaces):
# strings without their quotes, numbers/booleans/null as written. Empty when absent.
_ops_json_top() {
    local json="$1" field="$2" v
    # Quoted string first (it may contain commas), then a bare number/boolean/null.
    v="$(printf '%s\n' "$json" | sed -E -n "s/^  \"${field}\" *: *\"([^\"]*)\",?[[:space:]]*$/\1/p" | head -n1)"
    [[ -n "$v" ]] && { printf '%s' "$v"; return 0; }
    printf '%s\n' "$json" | sed -E -n "s/^  \"${field}\" *: *([^\",[:space:]]+),?[[:space:]]*$/\1/p" | head -n1
}

# _ops_json_any <json> <field> — the FIRST occurrence of a field at any depth.
_ops_json_any() {
    local json="$1" field="$2" v
    v="$(printf '%s\n' "$json" | sed -E -n "s/^[[:space:]]*\"${field}\" *: *\"([^\"]*)\",?[[:space:]]*$/\1/p" | head -n1)"
    [[ -n "$v" ]] && { printf '%s' "$v"; return 0; }
    printf '%s\n' "$json" | sed -E -n "s/^[[:space:]]*\"${field}\" *: *([^\",[:space:]]+),?[[:space:]]*$/\1/p" | head -n1
}

# _ops_ok <json> — the top-level "ok" is true. The presets (sysconfig set, balance add, …) and
# `tx smoke` all report it; the exit code alone is not used as the verdict because the output is
# what gets logged and read back by a person.
_ops_ok() {
    [[ "$(_ops_json_top "$1" ok)" == "true" ]]
}

# _ops_smoke_step <smoke_json> <step_name> <field> — one field of the named step in `tx smoke`'s
# steps array ({name, ok, txHash, status, detail}). jsoncpp sorts keys, so "detail" comes BEFORE
# "name" inside each step: the whole object is collected first and matched on close.
_ops_smoke_step() {
    local json="$1" step="$2" field="$3"
    printf '%s\n' "$json" | awk -v step="$step" -v field="$field" '
        /^[[:space:]]*\{/ { delete obj; inobj = 1; next }
        inobj && /^[[:space:]]*"[^"]+" *:/ {
            line = $0
            sub(/^[[:space:]]*"/, "", line); key = line; sub(/".*$/, "", key)
            sub(/^[^:]*: */, "", line); sub(/,[[:space:]]*$/, "", line); gsub(/^"|"$/, "", line)
            obj[key] = line; next
        }
        inobj && /^[[:space:]]*\}/ { if (obj["name"] == step) { print obj[field]; exit } inobj = 0 }'
}

# _ops_hex40_in <text> — the first 0x<40 hex> address in the text, anchored on a word boundary so a
# 64-hex transaction hash is never truncated into a fake address (the console-era bug
# tests/console_address_parse_test.sh guards against).
_ops_hex40_in() {
    printf '%s\n' "$1" | grep -oE '(^|[^0-9a-fA-F])0x[0-9a-fA-F]{40}([^0-9a-fA-F]|$)' | head -n1 | grep -oE '0x[0-9a-fA-F]{40}'
}

# _ops_smoke_contract <smoke_json> — the HelloWorld address `tx smoke` deployed (the deploy step's
# detail reads "contract 0x…").
_ops_smoke_contract() {
    _ops_hex40_in "$(_ops_smoke_step "$1" deploy detail)"
}

# ---------------------------------------------------------------------------
# IO: live chain only.
# ---------------------------------------------------------------------------

_ops_bin() {
    local b="${OPS_BIN:-${FISCO_BIN:-}}"
    [[ -n "$b" && -x "$b" ]] || {
        echo "ERROR: ops: no fisco-bcos binary to drive the chain with (set OPS_BIN, or FISCO_BIN to the binary under test); got '${b:-<empty>}'" >&2
        return 1
    }
    printf '%s' "$b"
}

_ops_node0() {
    printf '%s/node0' "${NODE_DIR:-./nodes-release-gate/127.0.0.1}"
}

# _ops_admin_pem — the genesis auth-admin key build_chain writes to <outdir>/ca/accounts/0x<40hex>.pem.
# It is the registered balance governor and the committee's first governor, so every write the
# engine issues signs with it.
_ops_admin_pem() {
    local accounts_dir pem
    accounts_dir="$(_ops_node0)/../../ca/accounts"
    [[ -d "$accounts_dir" ]] || { echo "ERROR: ops: $accounts_dir not found — build_chain writes the admin key there" >&2; return 1; }
    for pem in "$accounts_dir"/0x*.pem; do
        [[ -f "$pem" ]] || continue
        case "$pem" in *.pub.pem|*.public.pem) continue ;; esac
        [[ "$(basename "$pem" .pem)" =~ ^0x[0-9a-fA-F]{40}$ ]] && { printf '%s' "$pem"; return 0; }
    done
    echo "ERROR: ops: no 0x<40hex>.pem account in $accounts_dir" >&2
    return 1
}

# _ops_exec <args…> — run one subcommand against node0 with JSON output. Stdout is the JSON, stderr
# passes through (the binary prints "error: …" there), the return code is the binary's.
_ops_exec() {
    local bin
    bin="$(_ops_bin)" || return 1
    "$bin" "$@" --node-dir "$(_ops_node0)" --json --timeout "${OPS_TIMEOUT_MS:-60000}"
}

# _ops_read <args…> — a read (sysconfig get, status, block get, tx get): no signer, no prompt.
_ops_read() { _ops_exec "$@"; }

# _ops_wait_ready [timeout_s] — block until node0 answers a read, or fail after timeout_s (default
# 60). A node that was just (re)started takes a few seconds to open its RPC port; the first write
# after apply_profile.sh's restart used to land on "Connection refused" and fail the bring-up.
_ops_wait_ready() {
    local timeout="${1:-60}" waited=0 out
    while (( waited < timeout )); do
        if out="$(_ops_read sysconfig get tx_count_limit 2>/dev/null)" && [[ -n "$(_ops_json_top "$out" value)" ]]; then
            return 0
        fi
        sleep 2; waited=$((waited + 2))
    done
    echo "ERROR: ops: $(_ops_node0) did not answer sysconfig get within ${timeout}s" >&2
    return 1
}

# _ops_write <args…> — a governance write (sysconfig set, balance add, …): signs as the admin and
# answers the confirmation prompt, which otherwise exits 1 when stdin is not a terminal.
_ops_write() {
    local pem
    pem="$(_ops_admin_pem)" || return 1
    _ops_exec "$@" --account "$pem" --yes
}

# _ops_tx <args…> — `tx …`: signs as the admin (deploys need deploy authority on an auth chain);
# `tx` has no confirmation prompt and rejects --yes.
_ops_tx() {
    local pem
    pem="$(_ops_admin_pem)" || return 1
    _ops_exec "$@" --account "$pem"
}
