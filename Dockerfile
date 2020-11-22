FROM golang:1.14-alpine3.11 AS builder

ENV GOPROXY https://goproxy.io

WORKDIR /build
COPY go.mod .
COPY go.sum .

# 新增用户
RUN adduser -u 10001 -D app-runner
# 编译
COPY . .
COPY ./src ./src
RUN go mod download; \
    CGO_ENABLED=0 GOARCH=amd64 GOOS=linux go build -a -o run .;

FROM alpine:3.11 AS final

RUN apk add tzdata
WORKDIR /app
COPY --from=builder /build/run /app/
# 更新的数据
RUN wget http://data.haifengat.com/calendar.csv;

#USER app-runner
ENTRYPOINT ["./run"]
