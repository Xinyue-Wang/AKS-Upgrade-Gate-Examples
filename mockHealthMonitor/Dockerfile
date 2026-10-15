FROM --platform=$BUILDPLATFORM golang:1.23 AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY main.go ./
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /healthmonitor .

FROM gcr.io/distroless/static:nonroot
COPY --from=build /healthmonitor /healthmonitor
USER 65532:65532
ENTRYPOINT ["/healthmonitor"]
