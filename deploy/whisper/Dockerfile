# syntax=docker/dockerfile:1

# Portable whisper.cpp server for Vidra's optional "captions" profile.
#
# WHY THIS IMAGE EXISTS (issue: captions crash on common droplets):
# the upstream ghcr.io/ggml-org/whisper.cpp:main image is compiled with AVX-512.
# Common cloud CPUs (e.g. DigitalOcean "Regular" = AVX2/FMA, no AVX-512) hit an
# AVX-512 instruction on model load and die with SIGILL (exit 132) BEFORE the
# server ever binds — a "captions" profile that looks configured but never
# transcribes anything. This image is built for a PORTABLE x86-64 baseline
# (AVX2 + FMA + F16C = Intel Haswell / 2013+ and every AMD Zen), which runs on
# those droplets and essentially every 64-bit cloud host still in service, while
# staying fast enough for base-model transcription.
#
# It is a DROP-IN replacement for the upstream image: same ENTRYPOINT contract
# (["bash","-c"]) and the same `whisper-server` on PATH, so the compose command
#   whisper-server --host 0.0.0.0 --port 8080 --model /models/ggml-base.bin
# runs identically here.
#
# Reproducible: every input is a pinned build-arg (whisper.cpp tag, Ubuntu tag,
# model URL + SHA-256). CI passes the same values publish-whisper.yml declares.

ARG UBUNTU_TAG=24.04
ARG WHISPER_CPP_REF=v1.9.4

# ---- build stage: compile whisper-server for a portable AVX2 baseline --------
FROM ubuntu:${UBUNTU_TAG} AS build
ARG WHISPER_CPP_REF
RUN apt-get update && apt-get install -y --no-install-recommends \
        git cmake build-essential ca-certificates \
    && rm -rf /var/lib/apt/lists/*
RUN git clone --depth 1 --branch "${WHISPER_CPP_REF}" \
        https://github.com/ggml-org/whisper.cpp /src
WORKDIR /src
# GGML_NATIVE=OFF is the whole point. With it ON (the ggml default) the build
# compiles `-march=native` and bakes whatever the BUILD host's CPU supports — and
# a CI runner may well have AVX-512, which would reintroduce the exact SIGILL this
# image exists to remove. So NATIVE is off and the baseline is named explicitly:
# AVX + AVX2 + FMA + F16C on, every AVX-512 variant off. The emitted code is then
# identical no matter which CPU compiled it.
RUN cmake -B build \
        -DCMAKE_BUILD_TYPE=Release \
        -DGGML_NATIVE=OFF \
        -DGGML_AVX=ON -DGGML_AVX2=ON -DGGML_FMA=ON -DGGML_F16C=ON \
        -DGGML_AVX512=OFF -DGGML_AVX512_VBMI=OFF -DGGML_AVX512_VNNI=OFF \
        -DGGML_AVX512_BF16=OFF \
        -DBUILD_SHARED_LIBS=OFF \
        -DWHISPER_BUILD_TESTS=OFF -DWHISPER_BUILD_EXAMPLES=ON \
    && cmake --build build -j"$(nproc)" --config Release --target whisper-server \
    && cp build/bin/whisper-server /usr/local/bin/whisper-server \
    && strip /usr/local/bin/whisper-server

# ---- model stage: fetch + checksum-verify the ggml-base model ----------------
FROM ubuntu:${UBUNTU_TAG} AS model
# The default model + SHA-256 are the exact ggml-base the captions lab verified.
# Fail-closed: if the upstream file is moved or replaced, sha256sum -c exits
# non-zero and the build stops rather than baking an unverified model. Bump the
# URL and the SHA together, never one alone.
ARG WHISPER_MODEL_URL=https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-base.bin
ARG WHISPER_MODEL_SHA256=60ed5bc3dd14eea856493d334349b405782ddcaf0028d4b5df4088345fba2efe
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates wget \
    && rm -rf /var/lib/apt/lists/*
RUN set -eu; \
    mkdir -p /models; \
    wget -q -O /models/ggml-base.bin "${WHISPER_MODEL_URL}"; \
    echo "${WHISPER_MODEL_SHA256}  /models/ggml-base.bin" | sha256sum -c -

# ---- runtime stage: the server, its runtime libs, and the model --------------
FROM ubuntu:${UBUNTU_TAG} AS runtime
# libgomp1: whisper.cpp links OpenMP. wget: the compose healthcheck runs
# `wget -qO- http://localhost:8080/` INSIDE this container. ca-certificates:
# clean TLS for any runtime fetch. bash: the ENTRYPOINT and the compose command.
RUN apt-get update && apt-get install -y --no-install-recommends \
        libgomp1 wget ca-certificates \
    && rm -rf /var/lib/apt/lists/*
COPY --from=build /usr/local/bin/whisper-server /usr/local/bin/whisper-server
# Baked at the volume mount path on purpose: on first start with an EMPTY
# whisper_models named volume, Docker copies this file into the volume, so a
# fresh install transcribes without the old manual "wget into the volume" step.
# An operator who already populated the volume keeps their copy — Docker never
# overwrites a non-empty named volume.
COPY --from=model /models/ggml-base.bin /models/ggml-base.bin
EXPOSE 8080
# Same contract as ghcr.io/ggml-org/whisper.cpp: ["bash","-c"], so a single
# command string (the compose command) is run as one shell line with its flags
# intact. A LIST-form command would be swallowed as positional args to bash.
ENTRYPOINT ["bash", "-c"]
CMD ["whisper-server --host 0.0.0.0 --port 8080 --model /models/ggml-base.bin"]
