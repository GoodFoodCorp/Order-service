FROM golang:1.26-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o order-service ./cmd/main.go

FROM gcr.io/distroless/static-debian12

WORKDIR /

COPY --from=builder /app/order-service /order-service

USER nonroot:nonroot

EXPOSE 8082

ENTRYPOINT ["/order-service"]
