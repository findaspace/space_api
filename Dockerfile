FROM golang:1.27.2 AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .
ARG VERSION=dev
ARG COMMIT=none
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -trimpath \
      -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT}" \
      -o /out/ ./cmd/...

FROM debian:stable-slim AS worker

RUN apt-get update \
    && apt-get install -y --no-install-recommends ffmpeg ca-certificates \
    && rm -rf /var/lib/apt/lists/*

COPY --from=build /out/worker /worker

USER nobody
ENTRYPOINT ["/worker"]

FROM gcr.io/distroless/static:nonroot AS api
COPY --from=build /out/api /api
COPY --from=build /out/migrate /migrate
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/api"]

