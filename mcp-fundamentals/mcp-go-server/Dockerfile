# ---- Build stage ----
FROM golang:1.25-alpine AS builder

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o mcp-wikipedia-server .

# ---- Runtime stage ----
FROM alpine:3.21

RUN apk add --no-cache ca-certificates \
    && addgroup -S appgroup \
    && adduser -S appuser -G appgroup

WORKDIR /app

COPY --from=builder /build/mcp-wikipedia-server .
COPY --from=builder /build/suggested_titles.txt .

USER 10001

ENV MCP_TRANSPORT=http
ENV PORT=8080

EXPOSE 8080

ENTRYPOINT ["./mcp-wikipedia-server"]
