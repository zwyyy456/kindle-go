#!/bin/sh

LOG=/mnt/us/cloudclosure-5.15.1.log
TARGET=/app/KPPMainApp/js/KPPMainApp.js.hbc
OFFSET=1329719

{
  date
  echo "CloudClosure 5.15.1 apply"
  echo "Target: $TARGET"
  echo "Offset: $OFFSET"

  if [ ! -f "$TARGET" ]; then
    echo "ERROR: target file not found"
    eips 0 0 "CloudClosure: target missing" 2>/dev/null || true
    exit 1
  fi

  current="$(dd if="$TARGET" bs=1 skip="$OFFSET" count=1 2>/dev/null | hexdump -v -e '1/1 "%02x"')"
  echo "Current byte: $current"

  if [ "$current" = "fe" ]; then
    echo "Already patched"
    eips 0 0 "CloudClosure: already patched" 2>/dev/null || true
    exit 0
  fi

  if [ "$current" != "f8" ]; then
    echo "ERROR: byte mismatch; expected f8 or fe, got $current"
    eips 0 0 "CloudClosure: byte mismatch" 2>/dev/null || true
    exit 1
  fi

  mntroot rw
  printf '\376' | dd of="$TARGET" bs=1 seek="$OFFSET" count=1 conv=notrunc
  sync
  mntroot ro

  patched="$(dd if="$TARGET" bs=1 skip="$OFFSET" count=1 2>/dev/null | hexdump -v -e '1/1 "%02x"')"
  echo "Patched byte: $patched"

  if [ "$patched" != "fe" ]; then
    echo "ERROR: patch verification failed"
    eips 0 0 "CloudClosure: verify failed" 2>/dev/null || true
    exit 1
  fi

  echo "Patch applied; rebooting"
  eips 0 0 "CloudClosure: patched, rebooting" 2>/dev/null || true
  sync
  reboot
} >> "$LOG" 2>&1
