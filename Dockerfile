FROM golang:1.27.1-alpine@sha256:8a5910f31396cd4d89662f56c68b3ae31d374308270a1c3bd96672ee5ed43414 AS build
WORKDIR /src
COPY go.mod go.sum ./
COPY vendor ./vendor
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOPROXY=off GOTOOLCHAIN=local go build -mod=vendor -trimpath -ldflags='-s -w' -o /adtr ./cmd/adtr

FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /adtr /adtr
USER 65532:65532
ENTRYPOINT ["/adtr"]
