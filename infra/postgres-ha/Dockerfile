FROM postgres:16.16-bookworm@sha256:a49f7438ba03589f9b1ca062f2da82e899b48ea1dc50d2449fa09a70be83ebca

ARG TARGETARCH
ARG WALG_VERSION=v3.0.9
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates curl python3-venv liblzo2-2 libbrotli1 \
    && rm -rf /var/lib/apt/lists/*
COPY requirements.lock /tmp/requirements.lock
RUN python3 -m venv /opt/patroni \
    && /opt/patroni/bin/pip install --no-cache-dir --require-hashes -r /tmp/requirements.lock
RUN case "$TARGETARCH" in \
      amd64) arch=amd64; sum=d6795c663894d836ba20840bffe8970aed81bbe25d3151bd4951f6f9c362def9 ;; \
      arm64) arch=aarch64; sum=072eb4a4a119f817e1fa888c64a776b01e3453b2e0d967a67d149adb18fde652 ;; \
      *) echo "unsupported architecture: $TARGETARCH" >&2; exit 1 ;; \
    esac \
    && curl -fSL --retry 3 "https://github.com/wal-g/wal-g/releases/download/${WALG_VERSION}/wal-g-pg-22.04-${arch}" -o /usr/local/bin/wal-g \
    && echo "$sum  /usr/local/bin/wal-g" | sha256sum -c - \
    && chmod 0755 /usr/local/bin/wal-g \
    && wal-g --version
ENV PATH="/opt/patroni/bin:${PATH}"
COPY config/patroni.yml /etc/patroni.yml
COPY scripts/entrypoint.sh /usr/local/bin/ha-entrypoint
ENTRYPOINT ["/usr/local/bin/ha-entrypoint"]
CMD ["patroni", "/etc/patroni.yml"]
