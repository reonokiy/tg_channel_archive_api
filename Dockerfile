FROM golang:1.26-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -buildvcs=false -o /out/server ./cmd/server

FROM alpine:3.22

RUN adduser -D -H -u 10001 app && mkdir -p /data && chown app:app /data
USER app
WORKDIR /app
COPY --from=build /out/server /app/server

EXPOSE 8080
ENTRYPOINT ["/app/server"]
