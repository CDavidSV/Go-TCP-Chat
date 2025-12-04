FROM golang:1.25-alpine

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN go build -o /tcp-chat ./server

EXPOSE 3000

CMD ["/tcp-chat", "-host", "0.0.0.0"]
