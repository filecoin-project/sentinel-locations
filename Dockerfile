FROM golang:1.16-buster AS builder
MAINTAINER Hector Sanjuan <hector@protocol.ai>

ENV GOPATH      /go
ENV SRC_PATH    $GOPATH/src/github.com/filecoin-project/sentinel-locations
ENV GOPROXY     https://proxy.golang.org

# RUN apt-get update && apt-get install -y ca-certificates

COPY go.* $SRC_PATH/
WORKDIR $SRC_PATH
RUN go mod download

COPY . $SRC_PATH
RUN go install -trimpath -mod=readonly -ldflags "-X main.tag=$(git describe)"

#-------------------------------------------------------------------

#------------------------------------------------------
FROM busybox:1-glibc
MAINTAINER Hector Sanjuan <hector@protocol.ai>

ENV GOPATH      /go
ENV SRC_PATH    $GOPATH/src/github.com/filecoin-project/sentinel-locations

COPY --from=builder $GOPATH/bin/sentinel-locations /usr/local/bin/sentinel-locations
# COPY --from=builder /etc/ssl/certs /etc/ssl/certs

ENTRYPOINT ["/usr/local/bin/sentinel-locations"]
