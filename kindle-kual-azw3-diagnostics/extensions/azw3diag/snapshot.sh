#!/bin/sh

DIR=$(dirname "$0")
DIR=$(cd "$DIR" && pwd)
. "$DIR/common.sh"

ensure_base
load_run_dir

stamp=$(date +%Y%m%d-%H%M%S 2>/dev/null)
[ -n "$stamp" ] || stamp=snapshot

capture_state "$RUN/snapshot-$stamp.log" SNAPSHOT

cvm_pids=$(pidof cvm 2>/dev/null)
if [ -z "$cvm_pids" ]; then
    cvm_pids=$(ps w 2>/dev/null | awk '/[\/]cvm([[:space:]]|$)/ { print $1 }')
fi

{
    section "CVM thread dump request"
    date
    printf 'cvm_pids=%s\n' "$cvm_pids"
} >> "$RUN/snapshot-$stamp.log" 2>&1

# SIGQUIT asks the Java VM for a thread dump; it does not terminate the VM.
for cvm_pid in $cvm_pids; do
    kill -3 "$cvm_pid" 2>/dev/null
done

sleep 2
if [ -f /var/log/messages ]; then
    tail -n 4000 /var/log/messages > "$RUN/messages-snapshot-$stamp.log" 2>&1
fi
if command -v showlog >/dev/null 2>&1; then
    showlog -t > "$RUN/showlog-snapshot-$stamp.log" 2>&1
fi

sync_user_store
exit 0
