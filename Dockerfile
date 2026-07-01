# Build stage
FROM golang:1.22-bookworm AS build
WORKDIR /src
COPY go.mod ./
COPY . .
RUN CGO_ENABLED=0 go build -o /out/shift-server ./cmd/server

# Runtime stage — small image, just the binary + web assets
FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /app
COPY --from=build /out/shift-server ./shift-server
COPY --from=build /src/web ./web

# Railway sets $PORT at runtime; the app reads it via os.Getenv("PORT").
# SHIFT_DATA_PATH can point at a mounted Railway volume for persistence
# across deploys (see README "Deploying to Railway").
ENV SHIFT_DATA_PATH=/app/data/shift_data.json
RUN mkdir -p /app/data

EXPOSE 8080
CMD ["./shift-server"]
