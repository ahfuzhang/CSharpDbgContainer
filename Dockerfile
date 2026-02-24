FROM golang:1.26 AS debugadmin_builder

WORKDIR /src
COPY go.mod ./
COPY main.go ./
COPY internal ./internal

RUN apt-get update \
 && apt-get install -y --no-install-recommends wget unzip \
 && rm -rf /var/lib/apt/lists/* \
 && mkdir -p build \
 && wget -O ./build/speedscope-1.24.0.zip https://github.com/jlfwong/speedscope/releases/download/v1.24.0/speedscope-1.24.0.zip \
 && unzip -oq ./build/speedscope-1.24.0.zip -d ./build \
 && go build -o /out/DebugAdmin ./main.go


FROM linuxserver/code-server:latest

ARG DOTNET_VERSION=6.0

#####################
# 你想预装的插件列表（扩展ID）
ARG EXTENSIONS="\
  ms-dotnettools.vscode-dotnet-runtime \
  ms-dotnettools.csharp \
  ms-dotnettools.csdevkit \
  tamasfe.even-better-toml \
  ms-vscode.makefile-tools \
  jexus.netcoredbg \
  redhat.vscode-yaml \
"

WORKDIR /tmp/code-server
RUN apt-get update \
 && apt-get install -y --no-install-recommends curl ca-certificates unzip clang lld binutils zlib1g-dev wget make \
 && rm -rf /var/lib/apt/lists/*

USER root

COPY --from=debugadmin_builder /out/DebugAdmin /usr/bin/DebugAdmin

RUN curl -fsSL https://dot.net/v1/dotnet-install.sh -o dotnet-install.sh && \
    chmod +x dotnet-install.sh && \
    ./dotnet-install.sh --channel ${DOTNET_VERSION} --install-dir /usr/share/dotnet && \
    echo "get detail version:============================" && \
    version="$(basename $(find /usr/share/dotnet/sdk/ -type d -and -name "${DOTNET_VERSION}*" | head -n 1) )" && \
    ln -s /usr/bin/dotnetsdk/ /usr/share/dotnet/sdk/${version}/ && \
    echo "detail version = ${version}" && \
    echo "install vsdbg:============================" && \
    curl -sSL https://aka.ms/getvsdbgsh | /bin/sh /dev/stdin -v latest -l /usr/bin/vsdbg && \
    echo "install netcoredbg:============================" && \
    wget https://github.com/Samsung/netcoredbg/releases/download/3.1.3-1062/netcoredbg-linux-amd64.tar.gz && \
    tar -zxf netcoredbg-linux-amd64.tar.gz -C /usr/bin/ && \
    echo "install dotnet-trace:============================" && \
    case "$DOTNET_VERSION" in \
      6.0) dotnet_trace_version="--version 8.0.0" ;; \
      *)   dotnet_trace_version="" ;; \
    esac && \
    /usr/share/dotnet/dotnet tool install dotnet-trace ${dotnet_trace_version} --tool-path /usr/bin/dotnetsdk/ && \
    echo "install vscode extension:============================" && \
    export EXTENSIONS_GALLERY='{"serviceUrl":"https://marketplace.visualstudio.com/_apis/public/gallery"}' \
    && mkdir -p /config/extensions \
    && for ext in $EXTENSIONS; do \
        echo "Installing $ext"; \
        /app/code-server/bin/code-server \
            --extensions-dir /config/extensions \
            --install-extension "$ext" \
            --force \
        || { echo "ERROR: failed to install extension: $ext" >&2; exit 1; }; \
        done \
    && chown -R abc:abc /config

USER abc

ENV DOTNET_ROOT="/usr/share/dotnet"
ENV PATH="${DOTNET_ROOT}:/usr/bin/vsdbg:/usr/bin/netcoredbg:/usr/bin/dotnetsdk/:${PATH}"

COPY ./CodeServer/config.yaml /config/.config/code-server/config.yaml
COPY ./CodeServer/settings.json /config/.local/share/code-server/User/settings.json

WORKDIR /home/
ENTRYPOINT [""]
