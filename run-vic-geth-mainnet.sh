#!/usr/bin/env bash
# run-vic-geth-mainnet.sh — Run vic-geth on Viction mainnet from inside the vic-geth repo.
#
# Usage:
#   ./run-vic-geth-mainnet.sh [extra geth flags]
#   DATADIR=/other/datadir ./run-vic-geth-mainnet.sh
#
# Defaults target the sync box: datadir /md7/main-f1, log written to $DATADIR/sync.log.
#
# Unlike the monorepo-root script, this one lives in the repo itself, so it works on a
# server where only vic-geth has been cloned.
#
# It runs with --gcmode archive on purpose: block 8,505,900 investigation needs the state
# trie at block 8,505,899 to still be readable after the node stops.  Default gc keeps only
# ~128 recent blocks and would discard it.
#
# WARNING: this writes to DATADIR.  Point it at a copy if the original must stay untouched.

set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")"

# --- configuration (override via env) ---
DATADIR="${DATADIR:-/md7/main-f1}"
HTTP_PORT="${HTTP_PORT:-8555}"
WS_PORT="${WS_PORT:-8556}"
P2P_PORT="${P2P_PORT:-30303}"
GCMODE="${GCMODE:-archive}"
VERBOSITY="${VERBOSITY:-3}"
LOGFILE="${LOGFILE:-${DATADIR}/sync.log}"

# Prefer a prebuilt binary (servers), fall back to `go run` (dev machines).
# GETH_BIN=/path/to/geth or GO_BIN=/path/to/go override the detection.
GETH_BIN="${GETH_BIN:-}"
if [[ -z "$GETH_BIN" && -x ./build/bin/geth ]]; then
    GETH_BIN="./build/bin/geth"
fi
GO_BIN="${GO_BIN:-go}"

BOOTNODES="enode://98b06c30c631ab869c25b4836684b3de632ec7db60db34920095f8039cd49076913a64565dc4d8a06569bd84afa9553b95590ba3dd86263e3b9b5657463693a7@162.19.43.250:30301,\
enode://4c075010c7c1199240aea58a4e570b13af374a4aa2ed0219360c6017da34ca644706426ab2c75f1da00552ef4a40245ba043854d20e7c1873306023b6790bf03@15.235.228.11:30301,\
enode://477afcdf9581d0f38f00b2d0376bb536a3c71b13fcfa3d6039efb19c57a69e389b819efeb516609fe3eb3c90a8ffe620e62abaf481de6b07b873cad543a635fb@162.19.103.252:30301,\
enode://6a03f00972d02bc1f004fd05adf9384fb05a629e69ae894eebe1b55fe667a2313b595a180418f505948f99e5c66661229f8ea1d5c12aadb16433cae3122d1d8a@3.1.61.17:30301,\
enode://f799bf1da9f8b25891054913d876b1dcc301284fd001078239bcb5fb408078ac89741b8039f9adb0dfbfe72dca0b753e48099de3a059c3e23dc2402434ff8fd6@13.229.196.181:30301,\
enode://d3a79693bf18fd5136a4b1809193e28f94400b00a65a7f21c918876d986212c8031834b332f39fb8a814fb5e90f0e413fead6953d1aa23a839bba1224e285ed6@122.248.245.143:30301,\
enode://71f940f8672725d35e3f14b728dff228564e36cbe667c32a370cfe757016f2f1acb5a304c02410f3294b8f9dd4120075b23df87561a1dc765d502ebe1840010e@162.19.43.250:34343?discport=34343,\
enode://4937835b535f4f5713873841b2071b4eda7015c6c9ab328ce6c4e03e61d54a161d6a89d4e6b5087ba7229a09dc1369d8799fc63af933adc8586aa2cffd6ee15c@15.235.228.11:34343?discport=34343,\
enode://104542739bf7ed3369d924c8350c8ab5af0666880476189d20e63c9d1258edfa84b4aa9cf2f331711fd1726d1ac2ce64b10e649b1a8b1c7ef3aa8d0c7ffe45cf@162.19.103.252:30303?discport=34343"

mkdir -p "$DATADIR"

echo "==> Starting vic-geth on Viction mainnet"
if [[ -n "$GETH_BIN" ]]; then
    echo "    binary  : $GETH_BIN"
else
    echo "    binary  : go run ./cmd/geth (via $GO_BIN)"
fi
echo "    datadir : $DATADIR"
echo "    gcmode  : $GCMODE"
echo "    HTTP RPC: 0.0.0.0:$HTTP_PORT"
echo "    WS RPC  : 0.0.0.0:$WS_PORT"
echo "    P2P     : :$P2P_PORT"
[[ -n "$LOGFILE" ]] && echo "    log     : $LOGFILE"

ARGS=(
    --viction
    --datadir "$DATADIR"
    --port "$P2P_PORT"
    --maxpeers 50
    --bootnodes "$BOOTNODES"
    --syncmode full
    --gcmode "$GCMODE"
    --http
    --http.addr 0.0.0.0
    --http.port "$HTTP_PORT"
    --http.vhosts "*"
    --http.corsdomain "*"
    --http.api "eth,net,web3,debug,txpool"
    --ws
    --ws.addr 0.0.0.0
    --ws.port "$WS_PORT"
    --ws.origins "*"
    --cache.noprefetch
    --verbosity "$VERBOSITY"
    "$@"
)

if [[ -n "$GETH_BIN" ]]; then
    CMD=("$GETH_BIN" "${ARGS[@]}")
else
    CMD=("$GO_BIN" run ./cmd/geth "${ARGS[@]}")
fi

if [[ -n "$LOGFILE" ]]; then
    exec "${CMD[@]}" 2>&1 | tee -a "$LOGFILE"
fi
exec "${CMD[@]}"
