FROM golang:1.22-alpine AS builder

RUN apk add --no-cache gcc musl-dev

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY *.go ./
RUN go build -o myai main.go rag.go agents.go telegram.go db.go

FROM alpine:latest
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=builder /app/myai .

CMD ["./myai", "-tg"]
