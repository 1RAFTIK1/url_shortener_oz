# syntax=docker/dockerfile:1
FROM golang:1.26.5-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/shortener ./cmd/shortener

FROM gcr.io/distroless/static:nonroot

COPY --from=build /out/shortener /shortener

USER nonroot:nonroot
EXPOSE 8081

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD ["/shortener", "-healthcheck"]

ENTRYPOINT ["/shortener"]