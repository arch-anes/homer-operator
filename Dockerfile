FROM golang:1-alpine AS builder

WORKDIR /app
COPY main.go go.mod go.sum ./
RUN go mod tidy && go build && go test -v

FROM alpine:3

COPY --from=builder /app/homer-operator /usr/bin/homer-operator
ENTRYPOINT ["homer-operator"]
