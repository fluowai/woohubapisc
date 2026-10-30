# Multi-stage build for Whatsmeow Engine
FROM golang:1.24-alpine AS builder

WORKDIR /build

RUN apk add --no-cache git ca-certificates tzdata

COPY go.mod ./
# Download dependencies
RUN go mod tidy && go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-w -s" -o /build/whatsmeow .

# Production runner image
FROM alpine:3.20

WORKDIR /app

RUN apk add --no-cache ca-certificates tzdata ffmpeg

COPY --from=builder /build/whatsmeow /app/whatsmeow

ENV PORT=3001 \
    TZ=America/Sao_Paulo

EXPOSE 3001

CMD ["/app/whatsmeow"]