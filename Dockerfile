# syntax=docker/dockerfile:1
# Multi-arch image: linux/amd64, linux/arm64 (Raspberry Pi 4/5, 64-bit OS),
# linux/arm/v7 (32-bit Raspberry Pi OS).
#
#   docker run -d -p 8080:8080 --name archreg <user>/archreg
#
# Real detaljplaner (needs Lantmäteriet credentials), stored in a volume:
#   docker run --rm -v archreg-plans:/app/web/data/plans --env-file .env \
#     --entrypoint fetch-plans <user>/archreg -out /app/web/data/plans
#   docker run -d -p 8080:8080 -v archreg-plans:/app/web/data/plans <user>/archreg

FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# The WASM app is architecture-independent: build it once.
RUN GOOS=js GOARCH=wasm go build -trimpath -ldflags="-s -w" -o web/wasm/app.wasm ./cmd/wasm \
 && cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" web/wasm/ \
 && gzip -9 -k web/wasm/app.wasm
ARG TARGETOS TARGETARCH TARGETVARIANT
RUN GOARM="${TARGETVARIANT#v}" CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /out/ ./cmd/serve ./cmd/fetch-plans

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/ /usr/local/bin/
COPY --from=build --chown=nonroot:nonroot /src/web /app/web
WORKDIR /app
EXPOSE 8080
ENTRYPOINT ["serve", "-build=false", "-dir", "/app/web", "-addr", ":8080"]
