Update the QUIC transport to the fork used by Hysteria 2.13.0, bringing
retransmission-first stream scheduling and smaller ring-buffer retention.
Keep the existing congestion controllers and wire version 2. Align the desktop
release compiler with the mobile core's patched Go 1.26.6 toolchain.
