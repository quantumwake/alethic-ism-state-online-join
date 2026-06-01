# Build stage
FROM golang:1.25-alpine AS builder

ARG GIT_USERNAME
ARG GIT_TOKEN

RUN apk add --no-cache git sed

RUN echo -e "machine github.com\nlogin $GIT_USERNAME\npassword $GIT_TOKEN" > ~/.netrc

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o main .

# Runtime stage
FROM alpine:3

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app

COPY --from=builder /app/main .

EXPOSE 8080

CMD ["./main"]
