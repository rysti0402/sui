FROM golang:1.24-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o sui .

FROM scratch
LABEL org.opencontainers.image.source="https://github.com/rysti0402/sui"
LABEL org.opencontainers.image.description="SUI home server dashboard, maintained fork of willfantom/sui"
COPY ./templates /app/templates
COPY ./assets /app/assets
COPY --from=builder /app/sui /app/
WORKDIR /app
EXPOSE 6999
ENTRYPOINT [ "./sui" ]
