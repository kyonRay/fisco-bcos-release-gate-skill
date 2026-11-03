#!/usr/bin/env bash
# The pure half of ops_lib.sh: parsing the pretty-printed JSON the fisco-bcos subcommands write.
# Fixtures follow the shapes in bcos-ops (PresetCall.cpp PresetResult, Smoke.cpp, SysconfigCmd.cpp).
set -euo pipefail
cd "$(dirname "$0")/.."
source tests/assert.sh
source scripts/ops_lib.sh

# Verbatim shape from a 3.18.0 binary: jsoncpp styled output, spaces on both sides of the colon,
# keys sorted, arrays opening on the next line.
PRESET='{
  "blockNumber" : 7,
  "code" : 0,
  "codeName" : "SUCCESS",
  "decoded" : 
  [
    "0"
  ],
  "gasUsed" : "21000",
  "message" : "",
  "method" : "setValueByKey(string,string)",
  "mode" : "direct",
  "ok" : true,
  "params" : 
  [
    "feature_balance",
    "1"
  ],
  "status" : 0,
  "to" : "0x0000000000000000000000000000000000001000",
  "txHash" : "0xc6b6389b355db61bdaf28d2b8785f1774aa9a86cd70afd0a6d642d1300a121ce"
}'
assert_eq "true" "$(_ops_json_top "$PRESET" ok)" "top-level boolean"
assert_eq "7" "$(_ops_json_top "$PRESET" blockNumber)" "top-level number"
assert_eq "setValueByKey(string,string)" "$(_ops_json_top "$PRESET" method)" "top-level string, quotes stripped"
assert_eq "" "$(_ops_json_top "$PRESET" message)" "empty string value"
rc=0; _ops_ok "$PRESET" || rc=$?; assert_eq 0 "$rc" "_ops_ok on ok:true"

REFUSED='{
  "mode": "direct",
  "ok": false,
  "status": 16,
  "message": "Set compatibility version should not lower than version nowadays.",
  "proposal": {
    "ok": true,
    "status": 2
  }
}'
rc=0; _ops_ok "$REFUSED" || rc=$?; assert_eq 1 "$rc" "_ops_ok on ok:false"
assert_eq "false" "$(_ops_json_top "$REFUSED" ok)" "top-level ok is read, not the nested proposal.ok"
assert_eq "16" "$(_ops_json_top "$REFUSED" status)" "top-level status, not proposal.status"

GET='{
  "enableNumber" : 9,
  "key" : "tx_gas_price",
  "value" : "0x5208"
}'
assert_eq "0x5208" "$(_ops_json_top "$GET" value)" "sysconfig get value"

SMOKE='{
  "ok": true,
  "reason": "",
  "steps": [
    {
      "name": "deploy",
      "ok": true,
      "txHash": "0x6849f21d1e455e9f0712b1e99fa4fcd23758e8f16849f21d1e455e9f0712b1e9",
      "status": 0,
      "detail": "contract 0x272e28168a7df9dfc39d8399e3d559b3eb675916"
    },
    {
      "name": "set",
      "ok": true,
      "txHash": "0xaaaa389b355db61bdaf28d2b8785f1774aa9a86cd70afd0a6d642d1300a121ce",
      "status": 0,
      "detail": ""
    },
    {
      "detail" : "Hello, FISCO BCOS",
      "name" : "get",
      "ok" : true,
      "status" : 0,
      "txHash" : ""
    }
  ],
  "account": "0x9f10100f6b8bda11655e59f11b0905c009b31036"
}'
assert_eq "0x272e28168a7df9dfc39d8399e3d559b3eb675916" "$(_ops_smoke_contract "$SMOKE")" "deployed address from the deploy step"
assert_eq "0xaaaa389b355db61bdaf28d2b8785f1774aa9a86cd70afd0a6d642d1300a121ce" "$(_ops_smoke_step "$SMOKE" set txHash)" "set step txHash"
assert_eq "0" "$(_ops_smoke_step "$SMOKE" set status)" "set step status"
assert_eq "Hello, FISCO BCOS" "$(_ops_smoke_step "$SMOKE" get detail)" "get step detail"
assert_eq "" "$(_ops_smoke_step "$SMOKE" nosuch detail)" "unknown step is empty, not the first match"

# A 64-hex transaction hash must never be mistaken for a 40-hex address.
assert_eq "" "$(_ops_hex40_in "txHash 0x6849f21d1e455e9f0712b1e99fa4fcd23758e8f16849f21d1e455e9f0712b1e9")" "no address inside a 64-hex hash"
assert_eq "0x272e28168a7df9dfc39d8399e3d559b3eb675916" "$(_ops_hex40_in "contract 0x272e28168a7df9dfc39d8399e3d559b3eb675916")" "address when it stands alone"

# Static guard: no engine script shells out to the console any more.
assert_eq "0" "$(grep -l 'console\.sh' scripts/*.sh scripts/scenarios/*.sh 2>/dev/null | wc -l | tr -d ' ')" "no script invokes console.sh"
assert_done
