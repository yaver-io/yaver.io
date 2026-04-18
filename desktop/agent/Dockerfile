FROM golang:1.26-alpine AS builder
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /yaver .

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tmux
COPY --from=builder /yaver /usr/local/bin/yaver
ENTRYPOINT ["yaver"]
CMD ["serve"]
