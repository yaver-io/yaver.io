# syntax=docker/dockerfile:1.7

# Google's Linux Android SDK/NDK host tools are x86_64-only. Keep Java, Go,
# Node, Gradle, and Python native ARM64 while supplying the amd64 loader and
# runtime libraries required by a binfmt-enabled ARM64 release host.
FROM --platform=linux/amd64 ubuntu:24.04 AS amd64_runtime

FROM golang:1.26-bookworm AS go_runtime

FROM ghcr.io/cirruslabs/android-sdk:35

COPY --from=amd64_runtime /lib/x86_64-linux-gnu/ /lib/x86_64-linux-gnu/
COPY --from=amd64_runtime /lib64/ld-linux-x86-64.so.2 /lib64/ld-linux-x86-64.so.2
COPY --from=go_runtime /usr/local/go/ /usr/local/go/

ENV JAVA_HOME=/usr/lib/jvm/java-17-openjdk-arm64
ENV ANDROID_HOME=/opt/android-sdk-linux
ENV ANDROID_SDK_ROOT=/opt/android-sdk-linux
ENV PATH="/usr/local/go/bin:${JAVA_HOME}/bin:/opt/node/bin:${PATH}"

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
      ca-certificates curl git libxml2-utils openjdk-17-jdk-headless \
      openssl python3 python3-venv unzip \
    && yes | sdkmanager \
      "platforms;android-34" \
      "build-tools;34.0.0" \
      "cmake;3.22.1" \
      "ndk;27.0.12077973" \
      "ndk;27.1.12297006" \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/* \
    && install -d -m 700 /root/.androidplay /run/yaver-secrets \
    && java -version \
    && javac -version \
    && go version \
    && /opt/android-sdk-linux/build-tools/35.0.0/aapt2 version \
    && /opt/android-sdk-linux/cmake/3.22.1/bin/ninja --version \
    && /opt/android-sdk-linux/ndk/27.1.12297006/toolchains/llvm/prebuilt/linux-x86_64/bin/clang --version

RUN python3 -m venv /opt/yaver-play-python \
    && /opt/yaver-play-python/bin/pip install --no-cache-dir --upgrade pip \
    && /opt/yaver-play-python/bin/pip install --no-cache-dir \
      google-auth google-auth-httplib2 google-api-python-client

ENV PATH="/opt/yaver-play-python/bin:${PATH}"

WORKDIR /workspace
