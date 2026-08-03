# syntax=docker/dockerfile:1
# coinsphere Go 后端:多阶段构建 → 服务与迁移静态二进制。
# 构建上下文 = ./backend
FROM golang:1.26-alpine@sha256:0178a641fbb4858c5f1b48e34bdaabe0350a330a1b1149aabd498d0699ff5fb2 AS build
ARG TARGETOS=linux
ARG TARGETARCH=amd64
ARG GOPROXY=https://proxy.golang.org,direct
ENV GOTOOLCHAIN=local CGO_ENABLED=0 GOPROXY=${GOPROXY}
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,id=coinsphere-go-mod,target=/go/pkg/mod \
    go mod download
COPY . .
RUN --mount=type=cache,id=coinsphere-go-mod,target=/go/pkg/mod \
    --mount=type=cache,id=coinsphere-go-build,target=/root/.cache/go-build \
    GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -ldflags="-s -w" -o /out/coinsphere-server . \
    && GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -ldflags="-s -w" -o /out/coinsphere-migrate ./cmd/migrate

FROM scratch AS binaries
COPY --from=build /out/ /

FROM alpine:3.23@sha256:fd791d74b68913cbb027c6546007b3f0d3bc45125f797758156952bc2d6daf40
RUN apk add --no-cache ca-certificates tzdata \
    && adduser -D -H app
WORKDIR /app
COPY --from=build /out/coinsphere-server /app/coinsphere-server
COPY --from=build /out/coinsphere-migrate /app/coinsphere-migrate
COPY config.yml /app/config.yml
RUN mkdir -p /app/volumes/static /app/volumes/uploads \
    && chown -R app /app
USER app
EXPOSE 6987
CMD ["/app/coinsphere-server"]
