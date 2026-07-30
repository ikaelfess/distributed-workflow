FROM golang:1.26.0-bookworm
WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

EXPOSE 3000
CMD ["go", "run", "./cmd/iam"]
