FROM golang:1.23.3-alpine AS builder

RUN apk add --no-cache git

WORKDIR /app

COPY go.mod go.sum ./

RUN go mod download


COPY . .


RUN CGO_ENABLED=0 GOOS=linux go build -o phpscope .

FROM alpine:3.21 AS phpspy

# Install phpspy
RUN apk add --no-cache git make gcc g++ libc-dev && \
    git clone https://github.com/adsr/phpspy.git && \
    cd phpspy && \
    make

FROM alpine:latest

RUN apk add --no-cache binutils

COPY --from=builder /app/phpscope /usr/local/bin/

COPY --from=phpspy /phpspy/phpspy /usr/bin/phpspy

ENTRYPOINT ["phpscope"]

CMD ["--pyroscope", "http://localhost:4040", "--app", "local"] 