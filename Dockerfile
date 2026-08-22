FROM golang:1.26-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY *.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/homer-operator .

FROM alpine:3

COPY --from=builder /out/homer-operator /usr/local/bin/homer-operator
ENTRYPOINT ["/usr/local/bin/homer-operator"]
