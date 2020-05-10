FROM golang:buster@sha256:09b04534495af5148e4cc67c8ac55408307c2d7b9e6ce70f6e05f7f02e427f68 AS base
RUN mkdir /app
WORKDIR /app
ADD . .
RUN GOPATH=/app go get || /bin/true
RUN GOPATH=/app go build
EXPOSE 53/tcp
EXPOSE 53/udp
CMD ["sh"]
