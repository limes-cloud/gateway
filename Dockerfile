FROM golang:alpine AS build

#ENV GOPROXY=https://goproxy.cn,direct
ENV GO111MODULE=on
ENV GOTOOLCHAIN=auto
WORKDIR /go/cache
ADD go.mod .
ADD go.sum .
RUN go mod download

WORKDIR /go/build
ADD . .
RUN GOOS=linux CGO_ENABLED=0 go build -ldflags="-s -w" -installsuffix cgo -o gateway main.go

FROM alpine
WORKDIR /go/build

# 启动引导配置（镜像内嵌，保底：任何组件不可达时网关仍可启动）
COPY --from=build /go/build/config/ /go/build/config/
# 迁移脚本（建表/种子，部署时执行：docker run 挂载或 kubectl cp 后手工执行）
COPY --from=build /go/build/deploy/ /go/build/deploy/

COPY --from=build /go/build/gateway /go/build/gateway
CMD ["./gateway"]
