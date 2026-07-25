#!/bin/sh

LOG=/mnt/us/cloudclosure-5.15.1.log
TARGET=/app/KPPMainApp/js/KPPMainApp.js.hbc
OFFSET=1329719

{
  date
  echo "CloudClosure 5.15.1 restore"
  echo "Target: $TARGET"
  echo "Offset: $OFFSET"

  if [ ! -f "$TARGET" ]; then
    echo "ERROR: target file not found"
    eips 0 0 "CloudClosure: target missing" 2>/dev/null || true
    exit 1
  fi

  current="$(dd if="$TARGET" bs=1 skip="$OFFSET" count=1 2>/dev/null | hexdump -v -e '1/1 "%02x"')"
  echo "Current byte: $current"

  if [ "$current" = "f8" ]; then
    echo "Already restored"
    eips 0 0 "CloudClosure: already restored" 2>/dev/null || true
    exit 0
  fi

  if [ "$current" != "fe" ]; then
    echo "ERROR: byte mismatch; expected fe or f8, got $current"
    eips 0 0 "CloudClosure: byte mismatch" 2>/dev/null || true
    exit 1
  fi

  mntroot rw
  printf '\370' | dd of="$TARGET" bs=1 seek="$OFFSET" count=1 conv=notrunc
  sync
  mntroot ro

  restored="$(dd if="$TARGET" bs=1 skip="$OFFSET" count=1 2>/dev/null | hexdump -v -e '1/1 "%02x"')"
  echo "Restored byte: $restored"

  if [ "$restored" != "f8" ]; then
    echo "ERROR: restore verification failed"
    eips 0 0 "CloudClosure: verify failed" 2>/dev/null || true
    exit 1
  fi

  echo "Restore applied; rebooting"
  eips 0 0 "CloudClosure: restored, rebooting" 2>/dev/null || true
  sync
  reboot
} >> "$LOG" 2>&1
