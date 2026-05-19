# build stage
# This line is the critical change:
# It tells Docker to run this build stage using your Mac's native ARM64 architecture
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build

# --- No changes needed below here ---
COPY . /go/src/github.com/menionlabs/spot-termination-exporter
WORKDIR /go/src/github.com/menionlabs/spot-termination-exporter

# This command will now run natively on ARM64 and cross-compile to AMD64 (fast and stable)
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /bin/spot-termination-exporter .

# --- Final image (this will be AMD64) ---
FROM alpine:3.21
RUN apk update && apk add ca-certificates && rm -rf /var/cache/apk/*
COPY --from=build /bin/spot-termination-exporter /bin

USER nobody

ENTRYPOINT ["/bin/spot-termination-exporter"]
