# Етап збірки
FROM golang:1.23.3-alpine AS builder

# Встановлюємо необхідні інструменти для збірки
RUN apk add --no-cache git

# Встановлюємо робочу директорію
WORKDIR /app

# Копіюємо файли go.mod та go.sum
COPY go.mod go.sum ./

# Завантажуємо залежності
RUN go mod download

# Копіюємо весь код
COPY . .

# Збираємо додаток
RUN CGO_ENABLED=0 GOOS=linux go build -o phpscope .

FROM alpine:3.21 AS phpspy
# Install phpspy
RUN apk add --no-cache git make gcc g++ libc-dev && \
    git clone https://github.com/adsr/phpspy.git && \
    cd phpspy && \
    make

# Фінальний етап
FROM alpine:latest

# Встановлюємо часовий пояс
RUN apk add --no-cache binutils

# Копіюємо бінарний файл з етапу збірки
COPY --from=builder /app/phpscope /usr/local/bin/

COPY --from=phpspy /phpspy/phpspy /usr/bin/phpspy

# Встановлюємо точку входу
ENTRYPOINT ["phpscope"]

# Встановлюємо параметри за замовчуванням
# Їх можна перевизначити при запуску контейнера
CMD ["--pyroscope", "http://localhost:4040", "--app", "local"] 