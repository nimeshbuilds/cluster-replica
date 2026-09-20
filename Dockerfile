FROM --platform=$BUILDPLATFORM golang:1.27.1 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /operator ./cmd/operator

FROM scratch
LABEL org.opencontainers.image.source="https://github.com/nimeshbuilds/replicove" \
      org.opencontainers.image.title="Replicove" \
      org.opencontainers.image.description="Disposable Kubernetes integration environments powered by vCluster" \
      org.opencontainers.image.licenses="Apache-2.0"
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /operator /operator
USER 65532:65532
ENTRYPOINT ["/operator"]
