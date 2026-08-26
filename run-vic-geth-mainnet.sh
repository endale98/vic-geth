#!/usr/bin/env bash
# run-vic-geth-mainnet.sh — Run and control a vic-geth Viction mainnet node.
#
# Commands:
#   start [geth flags]    Start detached.  Survives logout.  Logs to $LOGFILE.
#   stop                  Graceful shutdown (SIGTERM), waits for the state flush.
#   watch                 Follow the log.  Ctrl-C stops watching, not the node.
#   attach                Open the geth JavaScript console over IPC.
#   status                PID, head block, peer count.
#   foreground [flags]    Run in this terminal (dies on logout).  For debugging.
#
# Environment overrides:
#   DATADIR HTTP_PORT WS_PORT P2P_PORT GCMODE VERBOSITY LOGFILE PIDFILE
#   GETH_BIN GO_BIN STOP_TIMEOUT
#
# --gcmode archive is the default on purpose: the block 8,505,900 investigation needs the
# state trie at 8,505,899 to still be readable after the node stops.  Default gc keeps only
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
PIDFILE="${PIDFILE:-${DATADIR}/vic-geth.pid}"
IPCFILE="${DATADIR}/geth.ipc"
STOP_TIMEOUT="${STOP_TIMEOUT:-600}"

# Prefer a prebuilt binary; `go run` only as a last resort (it runs geth as a child
# process, which breaks signal delivery and PID tracking when detached).
GETH_BIN="${GETH_BIN:-}"
if [[ -z "$GETH_BIN" && -x ./build/bin/geth ]]; then
    GETH_BIN="$(pwd)/build/bin/geth"
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

# Populate the global ARGS array with the geth command line, plus any extra flags passed in.
build_args() {
    ARGS=(
        --viction \
        --datadir "$DATADIR" \
        --port "$P2P_PORT" \
        --maxpeers 50 \
        --bootnodes "$BOOTNODES" \
        --syncmode full \
        --gcmode "$GCMODE" \
        --http \
        --http.addr 0.0.0.0 \
        --http.port "$HTTP_PORT" \
        --http.vhosts '*' \
        --http.corsdomain '*' \
        --http.api eth,net,web3,debug,txpool \
        --ws \
        --ws.addr 0.0.0.0 \
        --ws.port "$WS_PORT" \
        --ws.origins '*' \
        --cache.noprefetch \
        --verbosity "$VERBOSITY" \
        "$@"
    )
}

die() { echo "error: $*" >&2; exit 1; }

# Echo the running node's PID, or nothing.  The pidfile is authoritative when it points at a
# live process using this datadir; otherwise fall back to a scan, which also recovers the PID
# after a crash or a start from another shell.  Matching on DATADIR keeps other geth nodes on
# the same host out of scope.
node_pid() {
    local pid=""
    if [[ -f "$PIDFILE" ]]; then
        pid="$(cat "$PIDFILE" 2>/dev/null || true)"
        if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null &&
           ps -o args= -p "$pid" 2>/dev/null | grep -qF -- "$DATADIR"; then
            echo "$pid"
            return
        fi
    fi
    pgrep -f -- "geth.*--datadir $DATADIR( |$)" 2>/dev/null | head -1 || true
}

require_binary() {
    [[ -n "$GETH_BIN" ]] || die "no geth binary found. Run 'make geth' first, or set GETH_BIN=/path/to/geth"
    [[ -x "$GETH_BIN" ]] || die "GETH_BIN is not executable: $GETH_BIN"
}

# vic-geth stores its data in <datadir>/vic-geth (from clientIdentifier), not <datadir>/geth.
# Pointing at a datadir written by another client silently starts a fresh sync from genesis,
# so say so loudly rather than discovering it hours later.
check_instance_dir() {
    local mine="$DATADIR/vic-geth/chaindata"
    [[ -d "$mine" ]] && return 0
    local other
    for other in "$DATADIR"/*/chaindata; do
        [[ -d "$other" ]] || continue
        echo "WARNING: $mine does not exist, but $other does." >&2
        echo "         vic-geth will start a NEW sync from genesis." >&2
        echo "         To reuse it:  ln -s $(basename "$(dirname "$other")") $DATADIR/vic-geth" >&2
        return 0
    done
    return 0
}

cmd_start() {
    require_binary
    local running
    running="$(node_pid)"
    [[ -z "$running" ]] || die "already running (pid $running). Use 'stop' first, or 'watch' to follow it."

    mkdir -p "$DATADIR"
    check_instance_dir

    build_args "$@"

    # nohup makes the node ignore SIGHUP, so it outlives the login session.
    nohup "$GETH_BIN" "${ARGS[@]}" >>"$LOGFILE" 2>&1 </dev/null &
    local pid=$!
    sleep 1

    kill -0 "$pid" 2>/dev/null || die "node exited immediately. Check $LOGFILE"
    echo "$pid" > "$PIDFILE"

    echo "==> vic-geth started"
    echo "    pid     : $pid"
    echo "    datadir : $DATADIR"
    echo "    gcmode  : $GCMODE"
    echo "    log     : $LOGFILE"
    echo "    watch   : $0 watch"
    echo "    stop    : $0 stop"
}

cmd_stop() {
    local pid
    pid="$(node_pid)"
    if [[ -z "$pid" ]]; then
        echo "not running"
        rm -f "$PIDFILE"
        return 0
    fi

    # SIGTERM, not SIGINT: a process started in the background by a non-interactive shell
    # inherits SIGINT ignored, so 'stop' would hang forever waiting on a signal the node
    # never receives.  geth's handler treats both identically (cmd/utils/cmd.go:75).
    echo "==> stopping pid $pid (SIGTERM), waiting up to ${STOP_TIMEOUT}s for the state flush"
    kill -TERM "$pid"

    local waited=0
    while kill -0 "$pid" 2>/dev/null; do
        if (( waited >= STOP_TIMEOUT )); then
            echo "still running after ${STOP_TIMEOUT}s. Do NOT kill -9: that skips the state" >&2
            echo "flush and loses the archive state. Watch $LOGFILE and wait." >&2
            return 1
        fi
        sleep 2
        waited=$((waited + 2))
        (( waited % 20 == 0 )) && echo "    ... ${waited}s"
    done

    rm -f "$PIDFILE"
    echo "==> stopped cleanly"
}

cmd_watch() {
    [[ -f "$LOGFILE" ]] || die "no log at $LOGFILE"
    echo "==> following $LOGFILE (Ctrl-C stops watching, the node keeps running)"
    tail -f "$LOGFILE"
}

cmd_attach() {
    require_binary
    [[ -S "$IPCFILE" ]] || die "no IPC socket at $IPCFILE — is the node running?"
    exec "$GETH_BIN" attach "$IPCFILE"
}

cmd_status() {
    local pid
    pid="$(node_pid)"
    if [[ -z "$pid" ]]; then
        echo "vic-geth: not running"
        return 1
    fi
    echo "vic-geth: running (pid $pid)"
    echo "  datadir: $DATADIR"
    echo "  log    : $LOGFILE"
    if [[ -n "$GETH_BIN" && -S "$IPCFILE" ]]; then
        "$GETH_BIN" attach --exec \
            'console.log("  head   : " + eth.blockNumber + "\n  peers  : " + net.peerCount + "\n  syncing: " + JSON.stringify(eth.syncing))' \
            "$IPCFILE" 2>/dev/null || echo "  (IPC query failed — node may still be starting)"
    fi
}

cmd_foreground() {
    require_binary
    mkdir -p "$DATADIR"
    check_instance_dir
    build_args "$@"
    exec "$GETH_BIN" "${ARGS[@]}"
}

usage() {
    sed -n '/^# Commands:/,/^# WARNING/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
}

command="${1:-}"
[[ $# -gt 0 ]] && shift
case "$command" in
    start)      cmd_start "$@" ;;
    stop)       cmd_stop ;;
    watch|logs) cmd_watch ;;
    attach)     cmd_attach ;;
    status)     cmd_status ;;
    foreground) cmd_foreground "$@" ;;
    *)
        usage
        exit 2
        ;;
esac
