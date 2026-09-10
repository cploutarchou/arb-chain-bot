# Next.js console image (the repo has no web/Dockerfile; deploy.yml
# builds this one). Standalone output keeps the runtime image small;
# ARB_BACKEND_URL is read at start for the /api rewrite.
FROM node:22-alpine@sha256:c610fcdfb1d5b4740dd70c284ed3cb16bb857e0f7166196e36a5501df7a3aa32 AS deps
WORKDIR /app
COPY web/package.json web/package-lock.json ./
RUN npm ci --ignore-scripts

FROM node:22-alpine@sha256:c610fcdfb1d5b4740dd70c284ed3cb16bb857e0f7166196e36a5501df7a3aa32 AS build
WORKDIR /app
COPY --from=deps /app/node_modules ./node_modules
COPY web/ ./
ENV NEXT_TELEMETRY_DISABLED=1 NODE_ENV=production
RUN npm run build

FROM node:22-alpine@sha256:c610fcdfb1d5b4740dd70c284ed3cb16bb857e0f7166196e36a5501df7a3aa32
WORKDIR /app
ENV NODE_ENV=production NEXT_TELEMETRY_DISABLED=1 PORT=3000
RUN addgroup -g 1000 -S web && adduser -u 1000 -S -G web web \
 && mkdir -p /app/.next/cache && chown -R web:web /app
COPY --from=build --chown=web:web /app/.next ./.next
COPY --from=build --chown=web:web /app/package.json ./package.json
COPY --from=build --chown=web:web /app/next.config.ts ./next.config.ts
COPY --from=deps --chown=web:web /app/node_modules ./node_modules
USER web
EXPOSE 3000
HEALTHCHECK --interval=15s --timeout=3s CMD wget -qO- http://127.0.0.1:3000/ >/dev/null || exit 1
CMD ["npx", "next", "start", "-p", "3000"]
