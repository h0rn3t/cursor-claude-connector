# syntax=docker/dockerfile:1.6
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/cursor-claude-connector ./cmd/cursor-claude-connector

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/cursor-claude-connector /usr/local/bin/cursor-claude-connector
EXPOSE 9095
USER nonroot
ENTRYPOINT ["/usr/local/bin/cursor-claude-connector"]
