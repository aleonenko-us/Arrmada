# syntax=docker/dockerfile:1

# Memory. Docker builds the stages side by side, and with four cores this file used to
# peak at 6.5 GB: whisper.cpp's compilers (one of them 3.6 GB on its own, see the patch
# below) running next to the Go compiler and Intel's. On a small server one of them was
# killed part-way, and all Docker showed was the last line, e.g. "go build ... exit code 1".
#
# So the big compiles (the web UI, the Go binary and the two whisper.cpp builds) run
# through docker/build-heavy under one shared lock — the cache mount with
# id=arrmada-build-lock and sharing=locked, which one step holds at a time — and
# build-heavy fits each one's parallel jobs to the free memory. The image now builds in
# 2 GB (peak 2.0 GB measured; a cold build takes about 20% longer on four cores, since the
# compiles no longer overlap). ARRMADA_BUILD_JOBS (a build argument) fixes the job count.

# --- Stage 1: build the web UI ---
FROM node:24-alpine AS web
WORKDIR /src/web
COPY --chmod=755 docker/build-heavy /usr/local/bin/build-heavy
# Install deps first (cached unless the lockfile changes).
COPY web/package.json web/package-lock.json ./
RUN npm ci
# Build the SPA. Vite emits to ../internal/webui/dist (outside web/), so make it.
COPY web/ ./
ARG ARRMADA_BUILD_JOBS
RUN --mount=type=cache,id=arrmada-build-lock,target=/run/build-lock,sharing=locked \
    mkdir -p /src/internal/webui/dist && build-heavy 800 'npm run build'

# --- Stage 2: build the static Go binary (UI embedded) ---
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY --chmod=755 docker/build-heavy /usr/local/bin/build-heavy
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Overwrite the embed dir with the freshly built UI from stage 1.
COPY --from=web /src/internal/webui/dist ./internal/webui/dist
ARG VERSION=docker
ARG COMMIT=unknown
ARG ARRMADA_BUILD_JOBS
# CGO off → fully static (modernc.org/sqlite is pure Go), so it runs on scratch. The
# compiler's memory is mostly the one big SQLite package, so it isn't split into jobs.
RUN --mount=type=cache,id=arrmada-build-lock,target=/run/build-lock,sharing=locked \
    build-heavy 900 'CGO_ENABLED=0 go build \
    -ldflags "-s -w \
      -X github.com/tristenlammi/arrmada/internal/buildinfo.Version=${VERSION} \
      -X github.com/tristenlammi/arrmada/internal/buildinfo.Commit=${COMMIT}" \
    -o /out/arrmada ./cmd/arrmada'

# --- Stage 3: HDR tooling (musl static binaries for HDR10+ / Dolby Vision metadata) ---
FROM alpine:3.20 AS hdrtools
# Pinned, and checked: dovi_tool carries Dolby Vision through Convert's "keep" choice and
# hdr10plus_tool HDR10+ through every HEVC conversion, so a swapped release binary would
# write the metadata of every converted film. The releases publish no checksums, so these
# are the SHA-256 of the binaries inside those tarballs; a version bump updates them too.
ARG DOVI_VERSION=2.3.3
ARG DOVI_SHA256=871a0a5c44d1fd6baa6b972ec65167095e572f572d6c4da80c4b29483e4dfc1e
ARG HDR10PLUS_VERSION=1.7.2
ARG HDR10PLUS_SHA256=7845916b549c36e5d7fe9dbb3d24c124466d7c71ec3442e207551b677949d0be
RUN apk add --no-cache wget tar && set -eux; \
    wget -qO /tmp/dovi.tgz "https://github.com/quietvoid/dovi_tool/releases/download/${DOVI_VERSION}/dovi_tool-${DOVI_VERSION}-x86_64-unknown-linux-musl.tar.gz" && \
    mkdir -p /tmp/dovi && tar -xzf /tmp/dovi.tgz -C /tmp/dovi && \
    cp "$(find /tmp/dovi -type f -name dovi_tool | head -1)" /usr/local/bin/dovi_tool && \
    wget -qO /tmp/h10.tgz "https://github.com/quietvoid/hdr10plus_tool/releases/download/${HDR10PLUS_VERSION}/hdr10plus_tool-${HDR10PLUS_VERSION}-x86_64-unknown-linux-musl.tar.gz" && \
    mkdir -p /tmp/h10 && tar -xzf /tmp/h10.tgz -C /tmp/h10 && \
    cp "$(find /tmp/h10 -type f -name hdr10plus_tool | head -1)" /usr/local/bin/hdr10plus_tool && \
    echo "${DOVI_SHA256}  /usr/local/bin/dovi_tool" | sha256sum -c - && \
    echo "${HDR10PLUS_SHA256}  /usr/local/bin/hdr10plus_tool" | sha256sum -c - && \
    chmod +x /usr/local/bin/dovi_tool /usr/local/bin/hdr10plus_tool && \
    /usr/local/bin/dovi_tool --version && /usr/local/bin/hdr10plus_tool --version

# --- Stage 3b: whisper.cpp CLI (local AI subtitle generation) ---
# Built with the Vulkan backend so the GPU that already does hardware transcoding (Intel
# Arc / any Mesa- or proprietary-Vulkan card behind /dev/dri) also runs the AI. Vulkan
# is the one backend that covers Intel, AMD and NVIDIA from a single binary; on a host
# with no usable Vulkan device whisper-cli falls back to the CPU on its own, and the app
# retries with --no-gpu if it doesn't. Models are NOT baked in (multi-GB); the app
# downloads them on demand into the data dir.
# Built on Debian, matching the runtime: a musl-linked binary can't run on a glibc image.
FROM debian:bookworm-slim AS whisper
# v1.9.2+ matters: earlier builds didn't map timestamps back through VAD correctly
# (v1.8.4 fixed drift, v1.9.2 the token-level ones), which is what misaligned subtitles.
ARG WHISPER_VERSION=v1.9.3
RUN apt-get update && apt-get install -y --no-install-recommends \
        git cmake g++ make ca-certificates libvulkan-dev glslc spirv-headers && rm -rf /var/lib/apt/lists/*
# Bookworm's Vulkan headers (1.3.239) predate VK_EXT_layer_settings, which ggml's Vulkan
# backend now uses; the headers are header-only, so install a current set under
# /usr/local and point CMake at them. The loader (libvulkan-dev) stays the distro's.
ARG VULKAN_HEADERS_VERSION=v1.4.357
RUN git clone --depth 1 --branch ${VULKAN_HEADERS_VERSION} https://github.com/KhronosGroup/Vulkan-Headers /src/vk-headers && \
    cmake -S /src/vk-headers -B /src/vk-headers/build && \
    cmake --install /src/vk-headers/build --prefix /usr/local
# No trailing "|| true" on this chain: it used to swallow a failed build and produce an
# image with no whisper-cli in it, which the app then reported as "AI unavailable".
# The patch writes the embedded shaders as string literals, which g++ compiles in a
# fraction of the memory (3.6 GB → 0.4 GB for the biggest file; see the patch, which
# fails the build if it ever stops applying). The --param pair makes g++ collect its
# garbage sooner, as it would on a small machine: the largest remaining file,
# ggml-vulkan.cpp, then peaks at 1.5 GB instead of 1.8, for about 5% more time on it.
COPY docker/whisper-vulkan-shaders.patch /tmp/whisper-vulkan-shaders.patch
RUN git clone --depth 1 --branch ${WHISPER_VERSION} https://github.com/ggerganov/whisper.cpp /src/whisper && \
    cd /src/whisper && \
    git apply /tmp/whisper-vulkan-shaders.patch && \
    cmake -B build -DCMAKE_BUILD_TYPE=Release -DBUILD_SHARED_LIBS=OFF -DGGML_VULKAN=1 \
          -DVulkan_INCLUDE_DIR=/usr/local/include \
          -DCMAKE_CXX_FLAGS="--param ggc-min-expand=30 --param ggc-min-heapsize=65536"
COPY --chmod=755 docker/build-heavy /usr/local/bin/build-heavy
ARG ARRMADA_BUILD_JOBS
RUN --mount=type=cache,id=arrmada-build-lock,target=/run/build-lock,sharing=locked \
    cd /src/whisper && \
    build-heavy 1500 'cmake --build build -j"$JOBS"' && \
    cp build/bin/whisper-cli /usr/local/bin/whisper-cli && \
    strip /usr/local/bin/whisper-cli && \
    (ldd /usr/local/bin/whisper-cli || true)

# --- Stage 3c: whisper.cpp with Intel's oneAPI (SYCL) backend ---
# The Vulkan build never touches an Arc A-series card's XMX matrix units: ggml's Vulkan
# backend switches cooperative-matrix off for Alchemist outright ("performance
# regressions"), which left a 55-minute episode taking 38 minutes on an A380. SYCL is the
# backend Intel maintains: the whole model runs on the GPU through Level Zero with the
# matrix units in play. Same whisper.cpp tag, built with Intel's compiler; the app runs
# this build first and falls back to the Vulkan one when it fails or finds no GPU.
#
# The runtime libraries it needs (SYCL, oneMKL, oneDNN, the compiler runtime) are
# collected from ldd into one directory, so the runtime image carries a few hundred MB
# of libraries rather than the multi-GB toolkit. The Unified Runtime adapters are
# dlopen'd, not linked, so they're copied by name.
FROM intel/deep-learning-essentials:2025.3.3-0-devel-ubuntu24.04 AS whisper-sycl
ARG WHISPER_VERSION=v1.9.3
RUN apt-get update && apt-get install -y --no-install-recommends \
        git cmake make ca-certificates && rm -rf /var/lib/apt/lists/*
RUN git clone --depth 1 --branch ${WHISPER_VERSION} https://github.com/ggerganov/whisper.cpp /src/whisper && \
    cd /src/whisper && \
    (. /opt/intel/oneapi/setvars.sh --force >/dev/null 2>&1 || true) && \
    cmake -B build -DCMAKE_BUILD_TYPE=Release -DBUILD_SHARED_LIBS=OFF \
          -DGGML_SYCL=ON -DGGML_SYCL_F16=ON \
          -DCMAKE_C_COMPILER=icx -DCMAKE_CXX_COMPILER=icpx
COPY --chmod=755 docker/build-heavy /usr/local/bin/build-heavy
ARG ARRMADA_BUILD_JOBS
# About 0.6 GB per icpx job (2.1 GB with four).
RUN --mount=type=cache,id=arrmada-build-lock,target=/run/build-lock,sharing=locked \
    cd /src/whisper && \
    build-heavy 600 'cmake --build build -j"$JOBS" --target whisper-cli'
RUN cd /src/whisper && \
    mkdir -p /out/lib && cp build/bin/whisper-cli /out/whisper-cli && \
    ldd /out/whisper-cli | awk '/=> \/opt\/intel/{print $3}' | xargs -r -I{} cp -L {} /out/lib/ && \
    cp -L /opt/intel/oneapi/compiler/latest/lib/libur_adapter_level_zero*.so* \
          /opt/intel/oneapi/compiler/latest/lib/libur_adapter_opencl*.so* /out/lib/ && \
    # The adapters have dependencies of their own that the binary doesn't (libumf, for
    # one): sweep those too, repeating until nothing new turns up.
    for pass in 1 2 3; do \
        for f in /out/lib/*.so*; do ldd "$f" | awk '/=> \/opt\/intel/{print $3}'; done | sort -u \
            | xargs -r -I{} cp -Ln {} /out/lib/; \
    done && \
    for f in /out/whisper-cli /out/lib/libur_adapter_*.so.0; do \
        if LD_LIBRARY_PATH=/out/lib ldd "$f" | grep "not found"; then echo "$f: unresolved" >&2; exit 1; fi; \
    done && \
    du -sh /out/lib && ls -la /out/lib

# --- Stage 4: runtime ---
#
# Ubuntu 24.04 + jellyfin-ffmpeg, NOT Alpine's ffmpeg package. (Ubuntu rather than
# Debian since the SYCL build moved in: Intel publishes its Level Zero GPU driver for
# Ubuntu releases, and jellyfin-ffmpeg is built for both.)
#
# Alpine's libx265 segfaults at frame zero on some newer CPUs (reproduced on a Core Ultra
# 285K: the file decodes fine, libx264 encodes fine, VAAPI encodes fine, and libx265 dies
# instantly on every file even with asm=0). That removes CPU encoding entirely, and with it
# HDR conversion — every HDR metadata path encodes through x265.
#
# jellyfin-ffmpeg is built for exactly this job: verified working x265, VAAPI AND Quick Sync
# on the same hardware where Alpine's build failed all three (QSV had been exiting 171). It
# also bundles the Intel media drivers rather than depending on whatever the base image
# ships, which is one less thing to drift.
FROM ubuntu:24.04
# gosu is the su-exec equivalent: the entrypoint drops from root to a configurable PUID/PGID.
# apprise (Python) is bundled for notifications — one image, 80+ services, no extra container.
# jellyfin-ffmpeg7 brings libva and the Intel media drivers it needs for /dev/dri passthrough.
# libgomp1 is the Vulkan whisper-cli's OpenMP runtime; vainfo helps diagnose the GPU.
# libvulkan1 + mesa-vulkan-drivers give whisper-cli's Vulkan backend an Intel/AMD device
# through the same /dev/dri passthrough VAAPI uses; vulkaninfo (vulkan-tools) shows whether
# the container can actually see it.
# libze1 + libze-intel-gpu1 (Intel's Level Zero loader and GPU driver, from Intel's own
# repository) are what the SYCL whisper build talks to; intel-opencl-icd rides along so
# clinfo can show whether the compute side of the card is visible.
RUN apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates curl gnupg gosu python3 python3-pip libgomp1 vainfo \
        libvulkan1 mesa-vulkan-drivers vulkan-tools ocl-icd-libopencl1 clinfo && \
    mkdir -p /etc/apt/keyrings && \
    curl -fsSL https://repo.jellyfin.org/jellyfin_team.gpg.key \
        | gpg --dearmor -o /etc/apt/keyrings/jellyfin.gpg && \
    echo "deb [signed-by=/etc/apt/keyrings/jellyfin.gpg arch=amd64] https://repo.jellyfin.org/ubuntu noble main" \
        > /etc/apt/sources.list.d/jellyfin.list && \
    curl -fsSL https://repositories.intel.com/gpu/intel-graphics.key \
        | gpg --dearmor -o /etc/apt/keyrings/intel-graphics.gpg && \
    echo "deb [signed-by=/etc/apt/keyrings/intel-graphics.gpg arch=amd64] https://repositories.intel.com/gpu/ubuntu noble unified" \
        > /etc/apt/sources.list.d/intel-gpu.list && \
    apt-get update && apt-get install -y --no-install-recommends \
        jellyfin-ffmpeg7 libze1 libze-intel-gpu1 intel-opencl-icd && \
    rm -rf /var/lib/apt/lists/* && \
    ln -sf /usr/lib/jellyfin-ffmpeg/ffmpeg /usr/local/bin/ffmpeg && \
    ln -sf /usr/lib/jellyfin-ffmpeg/ffprobe /usr/local/bin/ffprobe && \
    pip3 install --no-cache-dir --break-system-packages apprise && \
    apprise --version && ffmpeg -version | head -1 && \
    mkdir -p /data /media/downloads /media/library /transcode

COPY --from=build /out/arrmada /usr/local/bin/arrmada
# Dolby Vision (dovi_tool) + HDR10+ (hdr10plus_tool) metadata extractors/injectors.
COPY --from=hdrtools /usr/local/bin/dovi_tool /usr/local/bin/hdr10plus_tool /usr/local/bin/
# whisper.cpp CLI for local AI subtitle generation (models downloaded on demand into /data/whisper).
COPY --from=whisper /usr/local/bin/whisper-cli /usr/local/bin/whisper-cli
# The oneAPI build and its libraries, behind a wrapper that sets the library path and
# device selection. The app prefers it and falls back to the Vulkan build above.
COPY --from=whisper-sycl /out/whisper-cli /opt/whisper-sycl/whisper-cli
COPY --from=whisper-sycl /out/lib /opt/whisper-sycl/lib
COPY docker/whisper-cli-sycl /usr/local/bin/whisper-cli-sycl
COPY docker/entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh /usr/local/bin/whisper-cli-sycl && \
    # Fail the build, not the first subtitle job, if a library the SYCL build needs
    # didn't make it across — the run-time-loaded adapters included, since a missing
    # dependency of theirs shows up only as "No device of requested type available".
    for f in /opt/whisper-sycl/whisper-cli /opt/whisper-sycl/lib/libur_adapter_*.so.0; do \
        if LD_LIBRARY_PATH=/opt/whisper-sycl/lib ldd "$f" | grep "not found"; then \
            echo "$f is missing shared libraries" >&2; exit 1; fi; \
    done && \
    whisper-cli-sycl --help >/dev/null 2>&1 && whisper-cli --help >/dev/null 2>&1

# Runs as root only long enough to fix data-dir ownership, then drops to PUID:PGID.
ENV ARRMADA_HOST=0.0.0.0 \
    ARRMADA_PORT=7878 \
    ARRMADA_DATA_DIR=/data \
    ARRMADA_LIBRARY_DIR=/media/library \
    ARRMADA_DOWNLOADS_DIR=/media/downloads \
    LIBVA_DRIVER_NAME=iHD \
    PUID=1000 \
    PGID=1000
EXPOSE 7878
VOLUME ["/data", "/media"]

# curl, not wget: the Ubuntu runtime has no wget, so the old check failed forever and
# Docker/Unraid showed the container as unhealthy. The first boot runs migrations, hence
# the minute's grace.
HEALTHCHECK --interval=30s --timeout=5s --start-period=60s --retries=3 \
    CMD curl -fsS -o /dev/null "http://127.0.0.1:7878/api/health" || exit 1

ENTRYPOINT ["/entrypoint.sh"]

# What update.sh reads before it calls into an image. org.arrmada.cli says the binary
# understands `arrmada <command>`: an older one ignores its arguments and starts a whole
# second server, so scripts never pass arguments to an image without this label.
LABEL org.arrmada.cli="1"
# This build can stage a restore itself (`arrmada restore`), which update.sh --rollback --with-db uses.
LABEL org.arrmada.restore="1"
# Declared last so a new commit only changes the image's metadata, never the cached
# layers above.
ARG VERSION=docker
ARG COMMIT=unknown
LABEL org.arrmada.version="${VERSION}" org.arrmada.commit="${COMMIT}"
