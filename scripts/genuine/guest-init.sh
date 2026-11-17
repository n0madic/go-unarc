#!/bin/sh
# PID 1 of the emulated i386 guest (see Dockerfile): mounts the shares of the runner, runs the job given
# after "--" on the kernel command line, prints its exit status and powers the machine off.
#
# Shares (9p): /job (read-only, the job scripts), /in (read-only, input files), /out (read-write).
export PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
export HOME=/root TZ=UTC LANG=C.UTF-8 WINEPREFIX=/wine WINEARCH=win32 WINEDEBUG=-all

mount -t proc proc /proc
mount -t sysfs sysfs /sys 2>/dev/null
mount -t devtmpfs devtmpfs /dev 2>/dev/null
mkdir -p /dev/pts /dev/shm
mount -t devpts devpts /dev/pts
mount -t tmpfs tmpfs /dev/shm
mount -t tmpfs tmpfs /tmp
mount -t tmpfs tmpfs /run
modprobe 9pnet_virtio
modprobe 9p
for tag in job in out; do
	mkdir -p /$tag
	mount -t 9p -o trans=virtio,version=9p2000.L,msize=262144 $tag /$tag || echo "genuine-init: cannot mount $tag"
done

cd /out && "$@"
status=$?
sync
echo "genuine-exit: $status"
# Power off. The disk goes away while the kernel shuts down, so wait with shell builtins only: PID 1
# must not exit or exec anything from now on (vm.sh also boots with panic=-1, so qemu quits either way).
echo o >/proc/sysrq-trigger
while :; do :; done
