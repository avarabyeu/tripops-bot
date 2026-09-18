# syntax=docker/dockerfile:1

# ----------------------------------------------------------------- builder --
# Pinned to the *build* platform. The bundle this stage emits is static files
# with no architecture, so only the nginx stage below has to match the target;
# building npm under emulation would cost minutes for nothing.
FROM --platform=$BUILDPLATFORM node:22-alpine AS builder

WORKDIR /app

COPY package.json package-lock.json* ./
RUN npm ci --no-audit --no-fund || npm install --no-audit --no-fund

COPY . .
RUN npm run build

# ----------------------------------------------------------------- runtime --
FROM nginx:1.27-alpine

# The Mini App is a single-page app served as static files, with /api proxied
# to the backend so the browser only ever sees one origin.
COPY --from=builder /app/dist /usr/share/nginx/html
COPY nginx.conf /etc/nginx/conf.d/default.conf

EXPOSE 80
