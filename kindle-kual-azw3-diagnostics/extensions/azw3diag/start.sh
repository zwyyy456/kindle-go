#!/bin/sh

DIR=$(dirname "$0")
DIR=$(cd "$DIR" && pwd)
. "$DIR/common.sh"

ensure_base

if [ -f "$PID_FILE" ]; then
    while IFS= read -r old_pid; do
        if [ -n "$old_pid" ] && kill -0 "$old_pid" 2>/dev/null; then
            exit 0
        fi
    done < "$PID_FILE"
fi

new_run_dir
capture_state "$RUN/initial-state.log" INITIAL

if [ -f /var/log/messages ]; then
    tail -n 2000 /var/log/messages > "$RUN/messages-before.log" 2>&1
    nohup tail -f /var/log/messages >> "$RUN/messages-live.log" 2>&1 &
    messages_pid=$!
else
    messages_pid=
fi

nohup "$DIR/monitor.sh" "$RUN" >/dev/null 2>&1 &
monitor_pid=$!

{
    printf '%s\n' "$monitor_pid"
    [ -n "$messages_pid" ] && printf '%s\n' "$messages_pid"
} > "$PID_FILE"

{
    printf 'run=%s\n' "$RUN"
    printf 'monitor_pid=%s\n' "$monitor_pid"
    printf 'messages_pid=%s\n' "$messages_pid"
} > "$RUN/capture-info.txt"

sync_user_store
exit 0
