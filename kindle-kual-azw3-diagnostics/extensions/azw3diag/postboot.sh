#!/bin/sh

DIR=$(dirname "$0")
DIR=$(cd "$DIR" && pwd)
. "$DIR/common.sh"

ensure_base
load_run_dir

stamp=$(date +%Y%m%d-%H%M%S 2>/dev/null)
[ -n "$stamp" ] || stamp=postboot
output="$RUN/postboot-$stamp.log"

capture_state "$output" POSTBOOT

copy_text_file /proc/last_kmsg "$output"

for pstore_file in /sys/fs/pstore/*; do
    [ -f "$pstore_file" ] || continue
    copy_text_file "$pstore_file" "$output"
done

if [ -f /var/log/messages ]; then
    tail -n 6000 /var/log/messages > "$RUN/messages-postboot-$stamp.log" 2>&1
fi
if [ -f /var/local/log/messages ]; then
    tail -n 6000 /var/local/log/messages > "$RUN/local-messages-postboot-$stamp.log" 2>&1
fi
if command -v showlog >/dev/null 2>&1; then
    showlog -t > "$RUN/showlog-postboot-$stamp.log" 2>&1
fi

{
    section "possible crash and watchdog evidence"
    for log_file in /var/log/messages /var/local/log/messages; do
        [ -f "$log_file" ] || continue
        grep -i -E 'oom|out of memory|killed process|watchdog|panic|segfault|fatal|signal|framework|cvm|index|mobi|reader|reboot' "$log_file" 2>&1
    done
} >> "$output" 2>&1

sync_user_store
exit 0
