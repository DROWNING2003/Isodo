ARG BASE_IMAGE=isodo-linux-base:0.1.0-dev
FROM ${BASE_IMAGE}

ENV DEBIAN_FRONTEND=noninteractive
ARG NODE_VERSION=v24.13.1
ARG NODE_ARCH=x64

RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates curl xz-utils \
    && rm -rf /var/lib/apt/lists/* \
    && cd /tmp \
    && archive="node-${NODE_VERSION}-linux-${NODE_ARCH}.tar.xz" \
    && curl -fsSLO "https://nodejs.org/dist/${NODE_VERSION}/${archive}" \
    && curl -fsSLO "https://nodejs.org/dist/${NODE_VERSION}/SHASUMS256.txt" \
    && grep " ${archive}$" SHASUMS256.txt | sha256sum -c - \
    && tar -xJf "$archive" -C /usr/local --strip-components=1 \
    && rm -f "$archive" SHASUMS256.txt \
    && node --version

ARG INSTALL_AGENT_CLIS=true
RUN if [ "$INSTALL_AGENT_CLIS" = "true" ]; then \
      npm install --global --prefix /opt/isodo-tools \
        @openai/codex@0.130.0 @anthropic-ai/claude-code@2.1.310 \
      && ln -sfn /opt/isodo-tools/bin/codex /usr/local/bin/codex \
      && ln -sfn /opt/isodo-tools/bin/claude /usr/local/bin/claude; \
    fi

COPY t3-server-dist.bundle /tmp/t3-server-dist.bundle
RUN mkdir -p /opt/isodo/t3 \
    && sha256sum /tmp/t3-server-dist.bundle | cut -d' ' -f1 > /opt/isodo/t3.bundle.sha256 \
    && tar -xzf /tmp/t3-server-dist.bundle --strip-components=1 -C /opt/isodo/t3 \
    && rm -f /tmp/t3-server-dist.bundle \
    && if [ -s /opt/isodo/t3/node_modules/@ff-labs/fff-bin-linux-${NODE_ARCH}-gnu/libfff_c.so.gz ]; then \
         gzip -d /opt/isodo/t3/node_modules/@ff-labs/fff-bin-linux-${NODE_ARCH}-gnu/libfff_c.so.gz; \
       fi \
    && chmod -R a+rX /opt/isodo/t3

COPY isodo-t3 /sbin/isodo-t3
COPY inittab /etc/inittab
RUN chmod 0555 /sbin/isodo-t3 && chmod 0444 /etc/inittab
