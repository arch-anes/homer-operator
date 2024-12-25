FROM golang:1-alpine AS builder

WORKDIR /app
COPY main.go go.mod go.sum ./
RUN go mod tidy && go build .

FROM b4bz/homer:latest

COPY --from=builder /app/homer-operator /usr/bin/homer-operator
ENTRYPOINT ["sh", "-c", "homer-operator & sh /entrypoint.sh"]
