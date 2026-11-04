#!/usr/bin/env bash
# gate.sh and run_case.sh stop their own chain unless the host passes RG_KEEP_CLUSTER=1, which is
# how `--keep-cluster` reaches them. Their stop calls sit behind a live chain, so the shape of each
# call site is pinned statically, and the case helper is run for real against a recording stub.
set -euo pipefail
cd "$(dirname "$0")/.."
source tests/assert.sh

assert_eq "1" "$(grep -c '"${RG_KEEP_CLUSTER:-0}" == 1' scripts/gate.sh)" "gate.sh checks the flag"
assert_eq "1" "$(grep -c 'stop_all.sh' <(sed -n '/RG_KEEP_CLUSTER:-0/,/^fi$/p' scripts/gate.sh))" "gate.sh's stop sits behind the check"
assert_eq "0" "$(grep -v '_case_stop_cluster\|RG_KEEP_CLUSTER' scripts/run_case.sh | grep -c 'stop_all.sh" ')" "run_case.sh stops only through its helper"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
printf '#!/bin/sh\necho stopped >> "%s/log"\n' "$tmp" > "$tmp/stop_all.sh"
eval "$(sed -n '/^_case_stop_cluster() {/,/^}/p' scripts/run_case.sh)"
NODE_DIR="$tmp"

RG_KEEP_CLUSTER=1 _case_stop_cluster
[[ ! -e "$tmp/log" ]]; assert_eq "0" "$?" "RG_KEEP_CLUSTER=1 leaves the chain up"
RG_KEEP_CLUSTER=0 _case_stop_cluster
assert_eq "stopped" "$(cat "$tmp/log")" "RG_KEEP_CLUSTER=0 stops it"
assert_done
