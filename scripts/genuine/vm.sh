#!/bin/sh
# Entry point of the go-unarc-freearc067 image: boots the i386 guest under qemu-system-i386 and runs
# "$@" in it (in /out). Bind-mount the job scripts at /job, the inputs at /in and the output folder at /out.
#
#   docker run --rm -v .../scripts/genuine:/job:ro -v .../inputs:/in:ro -v .../out:/out \
#       go-unarc-freearc067 sh /job/archives.sh
#
# GENUINE_MEM sets the guest RAM in MB and GENUINE_CPUS its CPU count: FreeArc 0.67 picks methods by both
# (multi-threaded "4x4" variants of the presets with more than one CPU). GENUINE_TIMEOUT (seconds) bounds
# the whole run.
set -eu
mem=${GENUINE_MEM:-3072}
cpus=${GENUINE_CPUS:-1}
[ $# -gt 0 ] || set -- sh

# A fresh root disk for every run: the guest changes it (Wine prefix, /tmp spill), the image stays clean.
size=$(( $(du -sm /guest | cut -f1) + 768 ))
mke2fs -q -t ext4 -L genuine -d /guest /tmp/root.img ${size}M

log=/tmp/console.log
# One host thread for all vCPUs (thread=single): x86 memory ordering stays exact on any host.
# No network: the guest needs none, and FreeArc stays offline.
timeout "${GENUINE_TIMEOUT:-10800}" qemu-system-i386 \
	-machine pc -accel tcg,thread=single -cpu max -smp "$cpus" -m "$mem" -nographic -no-reboot -nic none \
	-kernel /guest/vmlinuz -initrd /guest/initrd.img \
	-append "root=/dev/vda rw console=ttyS0 quiet loglevel=3 panic=-1 init=/sbin/genuine-init -- $*" \
	-drive file=/tmp/root.img,format=raw,if=virtio \
	-virtfs local,path=/job,mount_tag=job,security_model=none,readonly=on \
	-virtfs local,path=/in,mount_tag=in,security_model=none,readonly=on \
	-virtfs local,path=/out,mount_tag=out,security_model=none \
	</dev/null | tee "$log"

status=$(sed -n 's/^genuine-exit: \([0-9]*\).*/\1/p' "$log" | tail -n 1)
[ -n "$status" ] || { echo "genuine-vm: the guest did not report an exit status" >&2; exit 125; }
exit "$status"
