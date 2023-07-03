FROM golang:1.18
WORKDIR /app
COPY . .
RUN go mod download
RUN go get -t ./...
RUN go build -o main ./cmd/ethcompare
#Testnet api 1
#CMD ["./main", "txfilter", "--auth-header", "", "--gateway", "wss://uk.eth.blxrbdn.com/ws", "--trail-time", "600", "--num-intervals", "100"]
#Testnet api 2
CMD ["./main", "txfilter", "--auth-header", "", "--gateway", "wss://uk.eth.blxrbdn.com/ws", "--trail-time", "600", "--num-intervals", "100"]
#Local
#CMD ["./main", "txfilter", "--auth-header", "", "--gateway", "wss://uk.eth.blxrbdn.com/ws", "--trail-time", "600", "--num-intervals", "100"]
