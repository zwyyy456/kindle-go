#!/bin/sh

BASE=/mnt/us/azw3-diag
CURRENT_FILE="$BASE/current.path"
PID_FILE="$BASE/capture.pids"

ensure_base() {
    mkdir -p "$BASE" || exit 1
}

new_run_dir() {
    stamp=$(date +%Y%m%d-%H%M%S 2>/dev/null)
    [ -n "$stamp" ] || stamp=$(date +%s 2>/dev/null)
    [ -n "$stamp" ] || stamp=unknown-time

    RUN="$BASE/$stamp"
    suffix=0
    while [ -e "$RUN" ]; do
        suffix=$((suffix + 1))
        RUN="$BASE/$stamp-$suffix"
    done
    mkdir -p "$RUN" || exit 1
    printf '%s\n' "$RUN" > "$CURRENT_FILE"
}

load_run_dir() {
    RUN=
    if [ -f "$CURRENT_FILE" ]; then
        IFS= read -r RUN < "$CURRENT_FILE"
    fi
    if [ -z "$RUN" ] || [ ! -d "$RUN" ]; then
        new_run_dir
    fi
}

section() {
    printf '\n===== %s =====\n' "$1"
}

capture_state() {
    output=$1
    label=$2
    {
        section "$label date"
        date
        section "$label uname"
        uname -a
        section "$label uptime"
        uptime
        cat /proc/uptime 2>/dev/null
        section "$label version"
        cat /etc/pretty_version.txt 2>/dev/null
        cat /etc/version.txt 2>/dev/null
        section "$label cmdline"
        cat /proc/cmdline 2>/dev/null
        section "$label memory"
        free 2>&1
        cat /proc/meminfo 2>/dev/null
        section "$label filesystems"
        df -h 2>&1
        mount 2>&1
        section "$label processes"
        ps w 2>&1 || ps 2>&1
        section "$label top"
        top -b -n 1 2>&1
        section "$label dmesg"
        dmesg 2>&1
        section "$label log files"
        ls -l /var/log /var/local/log 2>&1
    } >> "$output" 2>&1
}

copy_text_file() {
    source_file=$1
    destination_file=$2
    if [ -f "$source_file" ]; then
        {
            section "$source_file"
            cat "$source_file"
        } >> "$destination_file" 2>&1
    fi
}

sync_user_store() {
    sync
}
