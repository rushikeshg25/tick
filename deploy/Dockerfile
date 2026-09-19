FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY . .
RUN CGO_ENABLED=0 go build -o /out/tickd ./cmd/tickd

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=build /out/tickd /usr/local/bin/tickd
ENTRYPOINT ["tickd"]
