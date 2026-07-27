# syntax=docker/dockerfile:1
# 前端:node 构建 dist → nginx 托管 SPA 并反代 /api、/static、/uploads、/ws、/health 到 Go 后端。
# 构建上下文 = ./frontend
FROM node:20-alpine AS build
WORKDIR /app
RUN npm i -g pnpm@9
COPY package.json pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile
COPY . .
# 生产环境 VITE_API_URL=/(同源),API 走 nginx 反代;若 vue-tsc 类型检查阻塞可改 `pnpm exec vite build`
RUN pnpm build

FROM nginx:1.27-alpine
COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build /app/dist /usr/share/nginx/html
EXPOSE 80
