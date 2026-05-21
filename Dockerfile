FROM golang:1.23-alpine AS builder
ENV GOTOOLCHAIN=auto
ENV CGO_ENABLED=1

RUN apk add --no-cache gcc musl-dev sqlite-dev

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY *.go ./
RUN go build -ldflags "-linkmode external -extldflags '-static'" -o myai main.go rag.go agents.go telegram.go db.go

FROM alpine:latest
RUN apk add --no-cache ca-certificates sqlite-libs
WORKDIR /app
COPY --from=builder /app/myai .
COPY static/ ./static/

CMD ["./myai", "-tg"]
