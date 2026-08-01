# syntax=docker/dockerfile:1
# coinsphere Go 后端:多阶段构建 → 单静态二进制(纯 Go sqlite,免 cgo)。
# 构建上下文 = ./backend
FROM golang:1.26-alpine AS build
ENV GOTOOLCHAIN=local CGO_ENABLED=0 GOOS=linux
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -trimpath -ldflags="-s -w" -o /out/coinsphere-server .

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata \
    && adduser -D -H app
WORKDIR /app
COPY --from=build /out/coinsphere-server /app/coinsphere-server
COPY config.yml /app/config.yml
RUN mkdir -p /app/data /app/volumes/static /app/volumes/uploads \
    && chown -R app /app
USER app
EXPOSE 6987
# sqlite 落在 /app/data,静态/上传在 /app/volumes(见 config.yml 与 main.go 路径解析)
CMD ["/app/coinsphere-server"]
