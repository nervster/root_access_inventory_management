# Production image for the NMS web app.

# 1. Build the static React files.
FROM node:22-alpine AS build
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci
COPY . .
# Vite builds the Clerk publishable key (public, pk_…) into the JavaScript.
ARG VITE_CLERK_PUBLISHABLE_KEY
RUN test -n "$VITE_CLERK_PUBLISHABLE_KEY" || (echo "Pass --build-arg VITE_CLERK_PUBLISHABLE_KEY=pk_..." && exit 1)
RUN npm run build

# 2. Serve them with nginx (runs as non-root, listens on 8080).
FROM nginxinc/nginx-unprivileged:alpine
COPY nginx.conf.template /etc/nginx/templates/default.conf.template
COPY --from=build /app/dist /usr/share/nginx/html
# Where /api requests go. In Kubernetes the ingress routes /api before it reaches nginx.
ENV API_UPSTREAM=http://backend:8080
EXPOSE 8080
