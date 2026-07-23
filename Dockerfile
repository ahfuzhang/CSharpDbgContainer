ARG CODE_SERVER_IMAGE=linuxserver/code-server:4.129.0
ARG DOTNET_VERSION=6.0
ARG SPEEDSCOPE_VERSION=1.24.0
ARG OSSUTIL_VERSION=2.3.0
ARG NETCOREDBG_VERSION=3.1.3-1062
ARG VECTOR_VERSION=0.53.0
ARG VSDBG_VERSION=18.7.10521.2

# 阶段：下载 speedscope 静态资源。
# 用于后续把火焰图前端文件 embed 进 DebugAdmin 二进制。
FROM golang:1.26 AS speedscope_fetcher
ARG SPEEDSCOPE_VERSION

WORKDIR /tmp/speedscope
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates wget unzip \
 && rm -rf /var/lib/apt/lists/* \
 && wget -O speedscope.zip "https://github.com/jlfwong/speedscope/releases/download/v${SPEEDSCOPE_VERSION}/speedscope-${SPEEDSCOPE_VERSION}.zip" \
 && unzip -oq speedscope.zip -d /out \
 && rm -f speedscope.zip

# 阶段：编译 DebugAdmin 管理程序。
# 这里产出最终要放进镜像的 DebugAdmin 二进制。
FROM golang:1.26 AS debugadmin_builder
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY main.go ./
COPY internal ./internal
COPY logging ./logging
COPY --from=speedscope_fetcher /out/speedscope ./build/speedscope

RUN GOOS=linux GOARCH=amd64 go build -o /out/DebugAdmin ./main.go

# 阶段：下载并整理 ossutil。
# 这里产出最终镜像里使用的 ossutil 二进制。
FROM debian:trixie-slim AS ossutil_fetcher
ARG OSSUTIL_VERSION

WORKDIR /tmp/ossutil
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates wget unzip \
 && rm -rf /var/lib/apt/lists/* \
 && wget -O ossutil.zip "https://gosspublic.alicdn.com/ossutil/v2/${OSSUTIL_VERSION}/ossutil-${OSSUTIL_VERSION}-linux-amd64.zip" \
 && unzip -oq ossutil.zip \
 && install -Dm755 "ossutil-${OSSUTIL_VERSION}-linux-amd64/ossutil" /out/ossutil \
 && rm -rf ossutil.zip "ossutil-${OSSUTIL_VERSION}-linux-amd64"

# 阶段：安装 DotNet SDK。
# 这里安装指定版本的 .NET SDK，供后续安装 dotnet CLI 工具并复制到最终镜像。
FROM ${CODE_SERVER_IMAGE} AS dotnet_sdk_builder
ARG DOTNET_VERSION

ENV DOTNET_ROOT=/opt/dotnet
ENV PATH="${DOTNET_ROOT}:${PATH}"
ENV DOTNET_CLI_TELEMETRY_OPTOUT=1
ENV DOTNET_SKIP_FIRST_TIME_EXPERIENCE=1

WORKDIR /tmp/dotnet
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl libicu74 \
 && rm -rf /var/lib/apt/lists/* \
 && curl -fsSL https://dot.net/v1/dotnet-install.sh -o dotnet-install.sh \
 && chmod +x dotnet-install.sh \
 && case "${DOTNET_VERSION}" in \
      6.0)  sdk_version="6.0.428" ;; \
      8.0)  sdk_version="8.0.423" ;; \
      10.0) sdk_version="10.0.302" ;; \
      *)    sdk_version="" ;; \
    esac \
 && if [ -n "${sdk_version}" ]; then \
      ./dotnet-install.sh --version "${sdk_version}" --install-dir "${DOTNET_ROOT}"; \
    else \
      ./dotnet-install.sh --channel "${DOTNET_VERSION}" --install-dir "${DOTNET_ROOT}"; \
    fi \
 && rm -f dotnet-install.sh

# 阶段：安装 dotnet CLI 工具。
# 这里安装 dotnet-trace、dotnet-coverage、dotnet-reportgenerator-globaltool。
FROM dotnet_sdk_builder AS dotnet_tools_builder
ARG DOTNET_VERSION

RUN mkdir -p /opt/dotnet-tools \
 && case "${DOTNET_VERSION}" in \
      6.0) dotnet_trace_version="8.0.452401"; dotnet_coverage_version="17.12.6"; reportgenerator_version="5.2.0" ;; \
      *)   dotnet_trace_version="";            dotnet_coverage_version="";        reportgenerator_version="" ;; \
    esac \
 && if [ -n "${dotnet_trace_version}" ]; then \
      ${DOTNET_ROOT}/dotnet tool install dotnet-trace --version "${dotnet_trace_version}" --tool-path /opt/dotnet-tools; \
    else \
      ${DOTNET_ROOT}/dotnet tool install dotnet-trace --tool-path /opt/dotnet-tools; \
    fi \
 && if [ -n "${dotnet_coverage_version}" ]; then \
      ${DOTNET_ROOT}/dotnet tool install dotnet-coverage --version "${dotnet_coverage_version}" --tool-path /opt/dotnet-tools; \
    else \
      ${DOTNET_ROOT}/dotnet tool install dotnet-coverage --tool-path /opt/dotnet-tools; \
    fi \
 && if [ -n "${reportgenerator_version}" ]; then \
      ${DOTNET_ROOT}/dotnet tool install dotnet-reportgenerator-globaltool --version "${reportgenerator_version}" --tool-path /opt/dotnet-tools; \
    else \
      ${DOTNET_ROOT}/dotnet tool install dotnet-reportgenerator-globaltool --tool-path /opt/dotnet-tools; \
    fi

# 阶段：安装 vsdbg 调试器。
# 这里产出 VS 调试协议用的 vsdbg 二进制目录。
FROM ${CODE_SERVER_IMAGE} AS vsdbg_builder
ARG VSDBG_VERSION
WORKDIR /tmp/vsdbg

RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl \
 && rm -rf /var/lib/apt/lists/* \
 && mkdir -p /out/vsdbg \
 && curl -sSL https://aka.ms/getvsdbgsh | /bin/sh /dev/stdin -v "${VSDBG_VERSION}" -l /out/vsdbg

# 阶段：下载 netcoredbg 调试器。
# 这里产出最终镜像里用于堆栈和调试的 netcoredbg 二进制目录。
FROM debian:trixie-slim AS netcoredbg_fetcher
ARG NETCOREDBG_VERSION

WORKDIR /tmp/netcoredbg
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates wget \
 && rm -rf /var/lib/apt/lists/* \
 && wget -O netcoredbg.tar.gz "https://github.com/Samsung/netcoredbg/releases/download/${NETCOREDBG_VERSION}/netcoredbg-linux-amd64.tar.gz" \
 && mkdir -p /out/netcoredbg \
 && tar -zxf netcoredbg.tar.gz -C /out/netcoredbg \
 && rm -f netcoredbg.tar.gz

# 阶段：下载 vector。
# 这里产出最终镜像里用于日志转发的 vector 二进制。
FROM debian:trixie-slim AS vector_fetcher
ARG VECTOR_VERSION

WORKDIR /tmp/vector
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates wget tar \
 && rm -rf /var/lib/apt/lists/* \
 && wget -O vector.tar.gz "https://github.com/vectordotdev/vector/releases/download/v${VECTOR_VERSION}/vector-${VECTOR_VERSION}-x86_64-unknown-linux-gnu.tar.gz" \
 && tar -zxf vector.tar.gz \
 && install -Dm755 vector-x86_64-unknown-linux-gnu/bin/vector /out/vector/bin/vector \
 && rm -rf vector.tar.gz vector-x86_64-unknown-linux-gnu

# 阶段：预装 code-server 扩展。
# 这里安装 C#、TOML、Makefile、YAML、netcoredbg 等 VS Code 扩展。
FROM ${CODE_SERVER_IMAGE} AS extensions_builder

#####################
# 你想预装的插件列表（扩展ID）
ARG EXTENSIONS="\
  ms-dotnettools.vscode-dotnet-runtime@3.1.0 \
  ms-dotnettools.csharp@2.146.2 \
  ms-dotnettools.csdevkit@3.27.196 \
  tamasfe.even-better-toml@0.21.2 \
  ms-vscode.makefile-tools@0.13.37 \
  jexus.netcoredbg@1.1.2 \
  redhat.vscode-yaml@1.25.2026072108 \
"

WORKDIR /tmp/extensions
RUN export EXTENSIONS_GALLERY='{"serviceUrl":"https://marketplace.visualstudio.com/_apis/public/gallery"}' \
 && mkdir -p /out/extensions \
 && for ext in $EXTENSIONS; do \
      echo "Installing $ext"; \
      /app/code-server/bin/code-server \
        --extensions-dir /out/extensions \
        --install-extension "$ext" \
        --force \
      || { echo "ERROR: failed to install extension: $ext" >&2; exit 1; }; \
    done

# 阶段：准备最终运行层的系统依赖。
# 这里安装 clang、lld、binutils、gdb、make、zlib1g-dev，以及 musl 编译链和 .NET 运行依赖。
FROM ${CODE_SERVER_IMAGE} AS runtime_base
WORKDIR /tmp/code-server

RUN apt-get update \
 && apt-get install -y --no-install-recommends \
    curl \
    ca-certificates \
    libicu74 \
    unzip \
    clang \
    lld \
    binutils \
    musl \
    musl-dev \
    musl-tools \
    zlib1g-dev \
    wget \
    make \
    gdb \
 && rm -rf /var/lib/apt/lists/*

# 阶段：组装最终镜像。
# 这里把 DebugAdmin、ossutil、.NET SDK、dotnet 工具、调试器、vector、code-server 扩展和配置汇总到最终镜像。
FROM runtime_base AS runtime
ARG DOTNET_VERSION

# 大而稳定的内容先写入镜像。这样小组件或应用程序变更时，
# SDK、调试器和扩展等大层能继续命中缓存并复用远端层。
COPY --from=extensions_builder --chown=abc:abc /out/extensions /config/extensions
COPY --from=dotnet_sdk_builder /opt/dotnet /usr/share/dotnet
COPY --from=vsdbg_builder /out/vsdbg /usr/bin/vsdbg
COPY --from=dotnet_tools_builder /opt/dotnet-tools /usr/bin/dotnetsdk
COPY --from=vector_fetcher /out/vector/bin/vector /usr/bin/vector

RUN version="$(basename "$(find /usr/share/dotnet/sdk/ -mindepth 1 -maxdepth 1 -type d -name "${DOTNET_VERSION}*" | head -n 1)")" \
 && test -n "${version}" \
 && ln -snf /usr/bin/dotnetsdk "/usr/share/dotnet/sdk/${version}/dotnetsdk"

# 小而稳定的内容随后写入镜像。
COPY --from=ossutil_fetcher /out/ossutil /usr/bin/ossutil
COPY --from=netcoredbg_fetcher /out/netcoredbg /usr/bin/netcoredbg
COPY --chown=abc:abc ./CodeServer/config.yaml /config/.config/code-server/config.yaml
COPY --chown=abc:abc ./CodeServer/settings.json /config/.local/share/code-server/User/settings.json

# DebugAdmin 是最常变化的内容，必须保持为最后一个文件系统层。
# 这样仅修改 Go 代码时，前面的 .NET SDK、调试器和 code-server 扩展层仍可复用。
COPY --from=debugadmin_builder /out/DebugAdmin /usr/bin/DebugAdmin

USER abc

ENV DOTNET_ROOT="/usr/share/dotnet"
ENV PATH="${DOTNET_ROOT}:/usr/bin/vsdbg:/usr/bin/netcoredbg:/usr/bin/dotnetsdk:${PATH}"

WORKDIR /home/
ENTRYPOINT [""]
