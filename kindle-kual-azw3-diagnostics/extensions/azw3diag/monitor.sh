#!/bin/sh

RUN=$1
[ -n "$RUN" ] || exit 1
[ -d "$RUN" ] || exit 1

while :; do
    {
        printf '\n===== sample %s =====\n' "$(date 2>/dev/null)"
        uptime 2>&1
        free 2>&1
        ps w 2>&1 || ps 2>&1
        printf '%s\n' '----- top -----'
        top -b -n 1 2>&1
        printf '%s\n' '----- recent dmesg -----'
        dmesg 2>&1 | tail -n 160
    } >> "$RUN/monitor.log" 2>&1
    sync
    sleep 2
done
