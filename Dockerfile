############################
# STEP 1 build executable binary
############################
FROM golang:alpine AS builder
# Install git.
# Git is required for fetching the dependencies.
RUN apk update && apk add --no-cache git

WORKDIR /app
COPY src/ .
COPY go.mod .
COPY go.sum .
# Fetch dependencies.
# Using go get.
RUN go mod download
RUN go mod verify
# Build the binary.
RUN go build -o /go/bin/app .

############################
# STEP 2 build a small image
############################
FROM alpine
# Copy our static executable.
COPY --from=builder /go/bin/app /go/bin/app
# Run the test-port binary.
ENTRYPOINT ["/go/bin/app"]