# syntax=docker/dockerfile:1
# 前端:node 构建 dist → nginx 托管 SPA 并反代 /api/v1、静态资源和健康检查到 Go 后端。
# 构建上下文 = ./frontend
FROM node:24-alpine@sha256:f70403e87646dc51b45295f4b8b70cdad0b63d2297c4c9899119b03f7af7a6b3 AS build
WORKDIR /app
RUN corepack enable && corepack prepare pnpm@10.33.0 --activate
COPY package.json pnpm-lock.yaml ./
RUN --mount=type=cache,id=coinsphere-pnpm-store,target=/root/.local/share/pnpm/store \
    pnpm install --frozen-lockfile
COPY . .
# 生产环境 VITE_API_URL=/(同源),API 走 nginx 反代。
ARG VITE_VERSION=development
ENV NODE_OPTIONS=--max-old-space-size=1536
RUN VITE_VERSION="${VITE_VERSION}" pnpm build

FROM scratch AS assets
COPY --from=build /app/dist/ /

FROM nginx:1.31-alpine@sha256:4a73073bd557c65b759505da037898b61f1be6cbcc3c2c3aeac22d2a470c1752
COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build /app/dist /usr/share/nginx/html
EXPOSE 80
