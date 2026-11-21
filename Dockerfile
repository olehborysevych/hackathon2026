# syntax=docker/dockerfile:1
# Multi-stage: build web (static export) -> embed into the Go binary -> distroless runtime.

FROM node:22-alpine AS web
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY server/ ./server/
COPY --from=web /web/out/ ./server/cmd/server/web/
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/server ./server/cmd/server

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/server /server
ENV PORT=8080
EXPOSE 8080
ENTRYPOINT ["/server"]
