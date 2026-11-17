# syntax=docker/dockerfile:1.7
#
# FreeArc 0.67 (the 32-bit Windows console build) under 32-bit Wine, in a whole emulated i386 PC.
#
# FreeArc's GHC runtime breaks under user-mode emulation: Arc.exe under Wine spins forever in "a" and "t"
# (qemu-i386 here, Rosetta + Wine on macOS), and the Linux build of 0.666 dies with "internal error:
# stg_ap_p_ret" or hangs. So the guest is a complete Debian i386 system (kernel, Wine, FreeArc) that
# qemu-system-i386 boots; the image itself is native (the build host's arch) and only runs qemu.
#
# Build: docker build -t go-unarc-freearc067 scripts/genuine   (build.sh does it)

# --- The guest: Debian 12 i386 with a kernel, Wine and FreeArc. Built under qemu-i386 user emulation,
# which is fine for apt and wineboot.
FROM --platform=linux/386 debian:bookworm-slim AS guest
ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update \
 && apt-get install -y --no-install-recommends linux-image-686 initramfs-tools kmod wine wine32 unzip \
 && rm -rf /var/lib/apt/lists/*

# FreeArc 0.67 (March 15 2014): the last release, published only as "0.67 alpha" in the testing folder.
# freearc.org is gone; the Wayback Machine has the files. Arc.exe in the portable zip is byte-identical
# to the one the console installer (FreeArc-console-0.67-alpha-win32.exe) installs; the zip is used
# because it unpacks without FreeArc. The files below are exactly the console installer's bin folder.
ADD --checksum=sha256:fc3d5d47b602441a16e56e363f954d08915baa9d257a814b305089a181c0be5b \
    https://web.archive.org/web/20151211000136id_/http://freearc.org/download/testing/0.67--2014-03-16/FreeArc-portable-0.67-alpha-win32.zip \
    /tmp/freearc.zip
RUN mkdir -p /opt/freearc \
 && cd /opt/freearc \
 && unzip -q -j /tmp/freearc.zip \
      bin/Arc.exe bin/arc.ini bin/arc.groups bin/facompress.dll bin/facompress_mt.dll bin/7z.dll \
      bin/unarc.exe bin/empty.arc bin/Descript.ion \
      bin/arc.sfx bin/arc-mini.sfx bin/arc-tiny.sfx bin/freearc.sfx bin/freearc-mini.sfx bin/freearc-tiny.sfx \
      bin/freearc-installer.sfx bin/freearc-installer-nodelete.sfx bin/7z.sfx bin/7zCon.sfx \
 && mkdir Codecs && unzip -q -j -d Codecs /tmp/freearc.zip bin/Codecs/FreeArcCodecs.dll \
 && echo "b36b1be0a3c329675af4eece3193f08cf343ede57a6933033bf6004a50ab2a65  Arc.exe" | sha256sum -c \
 && rm /tmp/freearc.zip

ENV WINEPREFIX=/wine WINEARCH=win32 WINEDEBUG=-all
RUN wineboot --init && wineserver --wait

# The guest boots straight into this script (init=) instead of a service manager.
COPY guest-init.sh /sbin/genuine-init
RUN chmod 755 /sbin/genuine-init \
 && ln -sf boot/$(cd /boot && ls vmlinuz-*) /vmlinuz \
 && ln -sf boot/$(cd /boot && ls initrd.img-*) /initrd.img

# --- The runner: qemu-system-i386 and the tools to turn the guest tree into a disk image.
FROM debian:trixie-slim
ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update \
 && apt-get install -y --no-install-recommends qemu-system-x86 e2fsprogs \
 && rm -rf /var/lib/apt/lists/*
COPY --from=guest / /guest/
COPY vm.sh /usr/local/bin/genuine-vm
RUN chmod 755 /usr/local/bin/genuine-vm
ENTRYPOINT ["/usr/local/bin/genuine-vm"]
