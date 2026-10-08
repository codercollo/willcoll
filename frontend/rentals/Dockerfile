# Rentals frontend (Nuxt) image. The build context is the pnpm WORKSPACE ROOT
# (frontend/), because the app depends on layers/base. Build from frontend/:
#
#   docker build -f rentals/Dockerfile -t willcoll-rentals-web .
#
# frontend/.dockerignore keeps node_modules, .nuxt, .output and .env* out of
# the context. Nothing is bind-mounted at run time.

# ---- build ---------------------------------------------------------------
FROM node:22-alpine AS build
WORKDIR /app
# pnpm at the version pinned in package.json ("packageManager").
RUN corepack enable

# Manifests first so the install layer is cached until a dependency changes.
# --ignore-scripts: the apps' postinstall (nuxt prepare) needs the source.
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
COPY layers/base/package.json layers/base/
COPY platform/package.json platform/
COPY rentals/package.json rentals/
RUN pnpm install --frozen-lockfile --ignore-scripts

COPY layers/ layers/
COPY rentals/ rentals/

RUN pnpm -r --if-present run postinstall && pnpm --filter rentals build

# ---- runtime ---------------------------------------------------------------
FROM node:22-alpine AS runtime
# API proxy targets are read at run time (NUXT_API_INTERNAL_BASE,
# NUXT_API_PLATFORM_INTERNAL_BASE): the same image runs in every environment.
ENV NODE_ENV=production HOST=0.0.0.0 PORT=3000
WORKDIR /app
# The Nitro output is self-contained: no node_modules, no pnpm.
COPY --from=build --chown=node:node /app/rentals/.output ./.output
USER node
EXPOSE 3000
CMD ["node", ".output/server/index.mjs"]
