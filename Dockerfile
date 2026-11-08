FROM golang:1.27.3-alpine3.23 AS builder

RUN apk add --no-cache git

WORKDIR /app

COPY go.mod go.sum ./

RUN go mod download


COPY . .


RUN CGO_ENABLED=0 GOOS=linux go build -o phpscope .

FROM alpine:3.23 AS phpspy

# PHP 8.4+ support is only on phpspy master (latest tag v0.7.0 predates it).
ARG PHPSPY_COMMIT=834d941c7d05e9a59f69914bdb4a8b2d41025ebb

RUN apk add --no-cache git make gcc g++ libc-dev && \
    git clone https://github.com/adsr/phpspy.git && \
    cd phpspy && \
    git checkout "$PHPSPY_COMMIT" && \
    make

FROM alpine:3.23

RUN apk add --no-cache binutils

COPY --from=builder /app/phpscope /usr/local/bin/

COPY --from=phpspy /phpspy/phpspy /usr/bin/phpspy

ENTRYPOINT ["phpscope"]

CMD ["--pyroscopeUrl", "http://localhost:4040", "--appName", "local"]
