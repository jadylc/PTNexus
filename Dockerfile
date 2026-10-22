# syntax=docker/dockerfile:1.7
# 阶段 1: 构建 Vue 前端（Go 版本）
FROM node:20-alpine AS webui-builder

WORKDIR /app/webui

RUN corepack enable

COPY ./webui/package.json ./webui/pnpm-lock.yaml ./
RUN --mount=type=cache,target=/pnpm/store \
    pnpm config set store-dir /pnpm/store && \
    pnpm install --frozen-lockfile

COPY ./webui ./
RUN --mount=type=cache,target=/pnpm/store \
    --mount=type=cache,target=/app/webui/node_modules/.vite \
    pnpm run build


# 阶段 2: 构建 Go 更新服务
FROM golang:1.23-bookworm AS updater-builder

WORKDIR /src/updater

COPY ./updater/go.mod ./updater/go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY ./updater/*.go ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /out/updater .


# 阶段 3: 构建 Go 后端（server）
# 注意：server 使用 sqlite（mattn/go-sqlite3），需要开启 CGO。
FROM golang:1.23-bookworm AS server-builder

WORKDIR /src/server

RUN apt-get -o Acquire::Retries=10 update && \
    apt-get -o Acquire::Retries=10 install -y --no-install-recommends gcc g++ libc6-dev && \
    rm -rf /var/lib/apt/lists/*

COPY ./server/go.mod ./server/go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY ./server/cmd ./cmd
COPY ./server/internal ./internal

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=1 GOOS=linux go build -ldflags="-s -w" -o /out/server ./cmd/server


# 阶段 4: 最终运行环境（纯 Go 运行时：updater 作为入口，反代到 server）
FROM debian:bookworm-slim

WORKDIR /app

# 注意：PTNEXUS_VERSION 这个与版本相关的 ARG 已下移到真正使用它的地方。
# ARG 声明在本层之上会让后续所有层（含 apt 安装层）的 BuildKit 缓存 key 随版本号变化，
# 导致每次发版都要重新联网跑一遍 apt-get install；镜像源抖动时会直接表现为
# “buildx failed ... exit code: 100”。

# 确保容器内对 localhost 和 127.0.0.1 的请求直接连接，不通过代理
ENV no_proxy="localhost,127.0.0.1,::1"
ENV NO_PROXY="localhost,127.0.0.1,::1"

# 禁用 updater 的定时自动更新
ENV SCHEDULE_ENABLED="false"

# server 运行目录与数据目录（对齐 updater/batch 使用的 /app/data）
ENV PTNEXUS_BASE_DIR="/app/server"
ENV PTNEXUS_DATA_DIR="/app/data"
ENV PTNEXUS_BDINFO_DIR="/app/bdinfo/linux"

# Debian 镜像源候选列表，按顺序尝试，全部失败才让构建失败。
# 流水线跑在 GitHub 海外 runner，官方源最稳；国内自建镜像可把国内源放前面：
#   docker build --build-arg APT_MIRROR_HOSTS="mirrors.aliyun.com deb.debian.org" ...
ARG APT_MIRROR_HOSTS="deb.debian.org mirrors.aliyun.com mirrors.tuna.tsinghua.edu.cn"

# 安装运行期依赖。
#   1) 单个镜像源抖动时自动切换下一个候选源，而不是整条流水线红掉；
#   2) 打开 apt 原生重试与超时（Acquire::Retries），吸收偶发的连接重置/半开下载；
#   3) deb 包与索引都走 BuildKit 缓存挂载，sharing=locked 避免 amd64/arm64 两个平台
#      并发构建时争抢同一份缓存导致 dpkg 状态错乱；
#   4) 去掉 --allow-insecure-repositories / --allow-unauthenticated：Debian 官方仓库签名可用，
#      这两个开关只会把真实的仓库故障伪装成安装失败；
#   5) 索引目录是缓存挂载，不会进入镜像，因此不再需要 rm -rf /var/lib/apt/lists/*。
RUN --mount=type=cache,target=/var/cache/apt,sharing=locked \
    --mount=type=cache,target=/var/lib/apt,sharing=locked \
    set -eu; \
    cp /etc/apt/sources.list.d/debian.sources /tmp/debian.sources.orig; \
    printf 'Acquire::Retries "10";\nAcquire::http::Timeout "30";\nAcquire::https::Timeout "30";\nAcquire::ForceIPv4 "true";\n' > /etc/apt/apt.conf.d/99-ptnexus-build; \
    use_mirror() { \
        cp /tmp/debian.sources.orig /etc/apt/sources.list.d/debian.sources; \
        sed -i -E \
            -e "s|https?://[A-Za-z0-9._-]+\.debian\.org/debian-security|http://$1/debian-security|g" \
            -e "s|https?://[A-Za-z0-9._-]+\.debian\.org/debian|http://$1/debian|g" \
            /etc/apt/sources.list.d/debian.sources; \
        grep -E '^URIs:' /etc/apt/sources.list.d/debian.sources || true; \
    }; \
    installed=0; \
    for mirror in $APT_MIRROR_HOSTS; do \
        echo ">>> 尝试 Debian 镜像源: ${mirror}"; \
        use_mirror "${mirror}"; \
        if apt-get update \
            && apt-get install -y --no-install-recommends \
                bash \
                ca-certificates \
                ffmpeg \
                mpv \
                mediainfo \
                util-linux \
                fonts-noto-cjk \
                libicu-dev \
                supervisor; then \
            installed=1; \
            echo ">>> Debian 依赖安装完成 (mirror=${mirror})"; \
            break; \
        fi; \
        echo ">>> 镜像源 ${mirror} 安装失败，切换到下一个候选源" >&2; \
        sleep 5; \
    done; \
    if [ "${installed}" != "1" ]; then \
        echo "!! 所有候选 Debian 镜像源均安装失败: ${APT_MIRROR_HOSTS}" >&2; \
        exit 1; \
    fi

# --- Go 版后端文件 ---
RUN mkdir -p /app/server
COPY --from=server-builder /out/server /app/server/server
RUN chmod +x /app/server/server

# 配置与站点数据（server 默认从 baseDir 下读取）
COPY ./server/configs /app/server/configs
COPY ./server/sites_data.json /app/server/sites_data.json

# --- Go 版前端产物：放入 server 的静态目录 ---
COPY --from=webui-builder /app/webui/dist /app/server/dist

# --- BDInfo（仅复制 Linux 工具，避免打入 Windows 版本）---
RUN mkdir -p /app/bdinfo/linux
COPY ./server/bdinfo/linux/ /app/bdinfo/linux/
RUN chmod +x /app/bdinfo/linux/BDInfo /app/bdinfo/linux/BDInfoDataSubstractor

# --- updater ---
COPY --from=updater-builder /out/updater /app/updater
RUN chmod +x /app/updater

# 复制版本文件（updater 默认读取 /app/CHANGELOG.json）
COPY ./CHANGELOG.json /app/CHANGELOG.json

# PTNEXUS_VERSION 只影响下面这一层：声明紧贴使用处、且放在依赖安装层之后，
# 这样发版号变化不会再把 apt 安装层踢出缓存。
ARG PTNEXUS_VERSION=""
RUN if [ -n "$PTNEXUS_VERSION" ]; then printf '%s\n' "$PTNEXUS_VERSION" > /app/VERSION; fi

# Supervisor + 启动脚本（Go 版）
COPY ./supervisord.conf /app/supervisord.conf
COPY ./start-services.sh /app/start-services.sh
RUN sed -i 's/\r$//' /app/start-services.sh && chmod +x /app/start-services.sh

# 创建数据目录，用于持久化存储（对齐原版镜像路径）
RUN mkdir -p /app/data /app/data/tmp
VOLUME /app/data

# 对外只暴露 updater 端口（入口反代）
EXPOSE 5274

CMD ["./start-services.sh"]
