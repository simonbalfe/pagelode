FROM golang:1.26.0-bookworm AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY signatures ./signatures
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/pagelode ./cmd/pagelode

FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/pagelode /usr/local/bin/pagelode
ENV PAGELODE_BROWSER_URL=http://browser:8090 \
    PAGELODE_STATE_PATH=/data/state.sqlite
VOLUME /data
EXPOSE 8083
ENTRYPOINT ["pagelode"]
CMD ["serve"]
