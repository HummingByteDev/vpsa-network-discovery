# Single Dockerfile for every platform component; select with COMPONENT.
#   docker build --build-arg COMPONENT=coordinator -t vapn/coordinator .
ARG COMPONENT=coordinator

FROM golang:1.26 AS build
ARG COMPONENT
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
ARG VERSION=dev
ARG COMMIT=unknown
RUN CGO_ENABLED=0 go build \
      -ldflags "-s -w \
        -X github.com/HummingByteDev/vpsa-network-discovery/internal/platform/version.Version=${VERSION} \
        -X github.com/HummingByteDev/vpsa-network-discovery/internal/platform/version.Commit=${COMMIT}" \
      -o /out/app ./cmd/${COMPONENT}

FROM gcr.io/distroless/static-debian12:nonroot AS runtime
ARG COMPONENT
COPY --from=build /out/app /app
# migrate needs the SQL files alongside the binary
COPY migrations/ /migrations/
ENV VAPN_MIGRATIONS_DIR=/migrations
ENTRYPOINT ["/app"]
