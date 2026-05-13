# syntax=docker/dockerfile:1.7

# Fedora-based image bundling Go 1.25.10 and KrakenD EE 2.13.3 for
# plugin development that targets a glibc/RHEL runtime. Go patch
# version MUST match the EE binary's own toolchain (go1.25.10) or
# the plugin .so will fail to load.

ARG FEDORA_VERSION=42
FROM --platform=linux/amd64 fedora:${FEDORA_VERSION}

ARG GO_VERSION=1.25.10
ARG GO_SHA256=42d4f7a32316aa66591eca7e89867256057a4264451aca10570a715b3637ba70
ARG KRAKEND_EE_VERSION=2.13.3

RUN dnf install -y --setopt=install_weak_deps=False \
        gcc \
        glibc-devel \
        make \
        tar \
        gzip \
        ca-certificates \
        curl \
        which \
        findutils \
    && dnf clean all

RUN curl -fsSLo /tmp/go.tgz "https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz" \
    && echo "${GO_SHA256}  /tmp/go.tgz" | sha256sum -c - \
    && tar -C /usr/local -xzf /tmp/go.tgz \
    && rm /tmp/go.tgz

RUN curl -fsSLo /tmp/krakend-ee.tgz \
        "https://download.krakend.io/bin/krakend-ee_${KRAKEND_EE_VERSION}_amd64_generic-linux.tar.gz" \
    && tar -C / -xzf /tmp/krakend-ee.tgz \
    && rm /tmp/krakend-ee.tgz

ENV PATH=/usr/local/go/bin:/root/go/bin:${PATH} \
    GOPATH=/root/go \
    CGO_ENABLED=1 \
    GOOS=linux \
    GOARCH=amd64

WORKDIR /src

CMD ["bash"]
