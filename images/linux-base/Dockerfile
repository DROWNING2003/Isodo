ARG BASE_IMAGE=ubuntu:22.04
FROM ${BASE_IMAGE}
ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update && apt-get install -y --no-install-recommends \
    bash busybox-static ca-certificates coreutils util-linux acpid iproute2 python3 git \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --gid 1000 isodo \
    && useradd --uid 1000 --gid 1000 --create-home --shell /bin/bash isodo \
    && passwd -l root && passwd -l isodo \
    && find / -xdev -type f -perm /6000 -exec chmod a-s {} + \
    && mkdir -p /workspace /etc/acpi/events \
    && chown isodo:isodo /workspace
COPY isodo-guest /usr/local/bin/isodo-guest
COPY isodo-init /sbin/isodo-init
COPY isodo-agent /sbin/isodo-agent
COPY inittab /etc/inittab
COPY power-button /etc/acpi/events/isodo-power-button
RUN chmod 0555 /usr/local/bin/isodo-guest /sbin/isodo-init /sbin/isodo-agent \
    && chmod 0444 /etc/acpi/events/isodo-power-button \
    && rm -f /etc/machine-id /var/lib/dbus/machine-id \
    && dpkg-query -W > /usr/share/isodo-packages.txt
ENTRYPOINT ["/sbin/isodo-init"]
