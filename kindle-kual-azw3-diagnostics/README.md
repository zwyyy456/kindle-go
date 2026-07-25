# Kindle AZW3 diagnostics

This KUAL extension captures Kindle framework, indexing, memory, process, and
kernel evidence around an AZW3 import/open failure. It does not modify the
Kindle system. All output is written to `/mnt/us/azw3-diag`.

## Install

Copy `extensions/azw3diag` to `/mnt/us/extensions/azw3diag` on the Kindle, then
ensure the shell files are executable:

```sh
chmod +x /mnt/us/extensions/azw3diag/*.sh
```

## Capture a reproduction

1. Remove the suspect book and its `.sdr` directory, then reboot once.
2. In KUAL, choose **AZW3 diagnostics > Start capture**.
3. Prefer copying the suspect AZW3 over SSH/SCP while capture is active. This
   avoids USB storage mode hiding the timing of the importer/indexer:

   ```sh
   scp suspect.azw3 root@KINDLE_IP:/mnt/us/documents/
   ```

4. Do not open the book yet. Observe whether import/indexing alone reboots the
   Kindle. If it remains alive but hangs, choose **Dump JVM and snapshot**.
5. If import survives, open the book and repeat **Dump JVM and snapshot** as
   soon as it starts hanging.
6. If the Kindle reboots, run **Collect after reboot** before importing the
   book again.
7. Copy the complete `/mnt/us/azw3-diag` directory back to the computer.

The same operations can be invoked over SSH:

```sh
/mnt/us/extensions/azw3diag/start.sh
/mnt/us/extensions/azw3diag/snapshot.sh
/mnt/us/extensions/azw3diag/postboot.sh
/mnt/us/extensions/azw3diag/stop.sh
```

`snapshot.sh` sends `SIGQUIT` to the Kindle Java VM to request a thread dump.
It does not kill the VM. Avoid leaving capture running for hours because the
periodic process snapshots consume storage.
