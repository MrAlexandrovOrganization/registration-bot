# check=skip=InvalidDefaultArgInFrom

# Required version arguments are supplied by Make/Compose, without fallback pins.
ARG GO_VERSION
ARG GO_ALPINE_VERSION
ARG ALPINE_VERSION
FROM golang:${GO_VERSION}-alpine${GO_ALPINE_VERSION} AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /telegram ./cmd/telegram

FROM alpine:${ALPINE_VERSION}
RUN apk add --no-cache ca-certificates && addgroup -g 10001 app && adduser -D -u 10001 -G app app
COPY --from=build /telegram /usr/local/bin/telegram
USER app
ENTRYPOINT ["telegram"]
