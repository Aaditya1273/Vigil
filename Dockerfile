FROM golang:1.25-alpine AS builder

WORKDIR /app

# Allow Go to auto-fetch a newer toolchain if any dependency requires it
ENV GOTOOLCHAIN=auto
ENV CGO_ENABLED=0
ENV GOOS=linux

COPY . .
RUN go mod download
RUN go build -ldflags="-w -s" -o vigil-server ./cmd/vigil-server

FROM alpine:3.20
RUN apk --no-cache add ca-certificates tzdata

WORKDIR /app
COPY --from=builder /app/vigil-server .

EXPOSE 8080

CMD ["/app/vigil-server"]
