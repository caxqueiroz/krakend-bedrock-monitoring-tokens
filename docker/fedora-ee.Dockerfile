# syntax=docker/dockerfile:1.7

# Fedora-based image bundling Go 1.25.9 and KrakenD EE 2.13.2 for
# plugin development that targets a glibc/RHEL runtime.

ARG FEDORA_VERSION=42
FROM --platform=linux/amd64 fedora:${FEDORA_VERSION}

ARG GO_VERSION=1.25.9
ARG GO_SHA256=00859d7bd6defe8bf84d9db9e57b9a4467b2887c18cd93ae7460e713db774bc1
ARG KRAKEND_EE_VERSION=2.13.2

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
