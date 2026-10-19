# Linux test bench: kernel WireGuard, nftables, iproute2 — the same tools May VPN uses on Linux.
FROM debian:bookworm-slim
RUN apt-get update -qq && apt-get install -y -qq --no-install-recommends \
      iproute2 nftables wireguard-tools python3 procps ca-certificates >/dev/null \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /work
