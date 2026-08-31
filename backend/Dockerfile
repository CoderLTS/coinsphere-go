# syntax=docker/dockerfile:1
# CoinSphere 单容器构建:Vue dist + Go 服务与迁移静态二进制。
# 构建上下文 = 仓库根目录
FROM node:24-alpine@sha256:f70403e87646dc51b45295f4b8b70cdad0b63d2297c4c9899119b03f7af7a6b3 AS web-build
WORKDIR /web
RUN corepack enable && corepack prepare pnpm@10.33.0 --activate
COPY frontend/package.json frontend/pnpm-lock.yaml ./
RUN --mount=type=cache,id=coinsphere-pnpm-store,target=/root/.local/share/pnpm/store \
    pnpm install --frozen-lockfile
COPY frontend/ .
ARG VITE_VERSION=development
ENV NODE_OPTIONS="--max-old-space-size=1920 --max-semi-space-size=8"
RUN VITE_VERSION="${VITE_VERSION}" pnpm build

FROM scratch AS web-assets
COPY --from=web-build /web/dist/ /

FROM golang:1.26.6-alpine@sha256:af8d6740070b8906d12eae1c3e3ea0957fb63f492051ea05e354c38ef9fe88df AS build
ARG TARGETOS=linux
ARG TARGETARCH=amd64
ARG GOPROXY=https://proxy.golang.org,direct
ENV GOTOOLCHAIN=local CGO_ENABLED=0 GOPROXY=${GOPROXY}
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN --mount=type=cache,id=coinsphere-go-mod,target=/go/pkg/mod \
    go mod download
COPY backend/ .
RUN --mount=type=cache,id=coinsphere-go-mod,target=/go/pkg/mod \
    --mount=type=cache,id=coinsphere-go-build,target=/root/.cache/go-build \
    GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -ldflags="-s -w" -o /out/coinsphere-server . \
    && GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -ldflags="-s -w" -o /out/coinsphere-migrate ./cmd/migrate \
    && GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -ldflags="-s -w" -o /out/coinsphere ./cmd/coinsphere

FROM scratch AS binaries
COPY --from=build /out/ /

FROM alpine:3.23@sha256:fd791d74b68913cbb027c6546007b3f0d3bc45125f797758156952bc2d6daf40
RUN apk add --no-cache ca-certificates tzdata \
    && adduser -D -H app
WORKDIR /app
COPY --from=build /out/coinsphere-server /app/coinsphere-server
COPY --from=build /out/coinsphere-migrate /app/coinsphere-migrate
COPY --from=build /out/coinsphere /app/coinsphere
COPY --from=web-build /web/dist /app/web
COPY backend/config.yml /app/config.yml
RUN mkdir -p /app/volumes/static /app/volumes/uploads /app/volumes/artifacts /app/web \
    && chown -R app /app
USER app
EXPOSE 6987
CMD ["/app/coinsphere-server"]
