FROM golang:alpine AS build
ENV GOPROXY=https://goproxy.cn,direct
ENV GO111MODULE on
WORKDIR /go/cache
ADD go.mod .
ADD go.sum .
RUN go mod download

WORKDIR /go/build
ADD . .
RUN GOOS=linux CGO_ENABLED=0 go build -ldflags="-s -w" -installsuffix cgo -o gateway cmd/gateway/main.go

FROM alpine
EXPOSE 7080
WORKDIR /go/build
COPY ./config/config.yaml /go/build/config/config.yaml
COPY --from=build /go/build/gateway /go/build/gateway
CMD ["./gateway","-c","config/config.yaml"]
