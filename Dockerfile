FROM golang:1.25.5-bookworm AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/pagelode ./cmd/pagelode

FROM oven/bun:1.3.5-debian

WORKDIR /app
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates chromium && rm -rf /var/lib/apt/lists/*
COPY browser/package.json browser/bun.lock browser/tsconfig.json ./browser/
RUN cd browser && bun install --frozen-lockfile && bunx patchright install --with-deps chromium
COPY browser/src ./browser/src
COPY --from=build /out/pagelode /usr/local/bin/pagelode
ENV PAGELODE_PATCHRIGHT_COMMAND=bun
ENV PAGELODE_PATCHRIGHT_WORKER=/app/browser/src/worker.ts
EXPOSE 8083
ENTRYPOINT ["pagelode"]
CMD ["serve"]
