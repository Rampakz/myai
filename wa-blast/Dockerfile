FROM golang:1.25-alpine AS builder
RUN apk add --no-cache gcc musl-dev
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=1 GOOS=linux go build -o wa-blast .

FROM alpine:latest
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=builder /app/wa-blast .
COPY web/ ./web/
RUN mkdir -p /data
ENV DATA_DIR=/data
ENV PORT=8080
EXPOSE 8080
CMD ["./wa-blast"]
