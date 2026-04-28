FROM golang:1.25-alpine AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . ./
RUN CGO_ENABLED=0 go build -mod=mod -o pihole-docker-dns .

FROM alpine:3.19

COPY --from=builder /src/pihole-docker-dns /pihole-docker-dns
COPY config.yaml /config.yaml

ENTRYPOINT ["/pihole-docker-dns"]
CMD ["-config", "/config.yaml"]