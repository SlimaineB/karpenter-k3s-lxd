FROM ubuntu:24.04 AS builder
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl
COPY k3s-lxd-provider /usr/bin/k3s-lxd-provider

FROM ubuntu:24.04
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates curl \
    && rm -rf /var/lib/apt/lists/*

COPY --from=builder /usr/bin/k3s-lxd-provider /usr/bin/k3s-lxd-provider

ENTRYPOINT ["/usr/bin/k3s-lxd-provider"]
