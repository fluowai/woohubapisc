FROM golang:1.27-alpine AS builder

WORKDIR /src
RUN apk add --no-cache git ca-certificates tzdata

COPY . .
WORKDIR /src/woohub-engine
RUN go mod tidy && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags="-w -s" -o /out/woohub-engine .

FROM alpine:3.20
WORKDIR /app
RUN apk add --no-cache ca-certificates tzdata ffmpeg
COPY --from=builder /out/woohub-engine /app/woohub-engine
ENV PORT=3001 TZ=America/Sao_Paulo
EXPOSE 3001
CMD ["/app/woohub-engine"]
