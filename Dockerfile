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
RUN mkdir -p /out/work
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
# Scratch space for the builder's RIS bview download. Production mounts a named
# volume here; Docker seeds a fresh volume from the image, so the directory must
# exist AND be owned by the distroless nonroot user (65532) or the first build
# fails with "open /work/.bview-*: permission denied".
COPY --from=build --chown=65532:65532 /out/work/ /work/
ENV VAPN_MIGRATIONS_DIR=/migrations
ENTRYPOINT ["/app"]
