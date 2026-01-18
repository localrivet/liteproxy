FROM golang:1.24-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o liteproxy .

FROM alpine:3.19

RUN apk add --no-cache ca-certificates

COPY --from=builder /app/liteproxy /usr/local/bin/liteproxy

EXPOSE 80 443

ENTRYPOINT ["liteproxy"]
