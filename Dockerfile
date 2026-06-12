# Build stage
FROM golang:1.26-alpine AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /bin/api ./cmd/api

# Runtime stage — distroless static, runs as nonroot
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /bin/api /api
EXPOSE 8080
ENTRYPOINT ["/api"]
