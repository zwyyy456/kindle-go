#!/bin/sh

DIR=$(dirname "$0")
DIR=$(cd "$DIR" && pwd)
. "$DIR/common.sh"

ensure_base
load_run_dir

if [ -f "$PID_FILE" ]; then
    while IFS= read -r capture_pid; do
        if [ -n "$capture_pid" ] && kill -0 "$capture_pid" 2>/dev/null; then
            kill "$capture_pid" 2>/dev/null
        fi
    done < "$PID_FILE"
    rm -f "$PID_FILE"
fi

capture_state "$RUN/final-state.log" FINAL
if [ -f /var/log/messages ]; then
    tail -n 4000 /var/log/messages > "$RUN/messages-final.log" 2>&1
fi

sync_user_store
exit 0
