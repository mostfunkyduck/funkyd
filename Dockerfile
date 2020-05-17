FROM golang:buster@sha256:09b04534495af5148e4cc67c8ac55408307c2d7b9e6ce70f6e05f7f02e427f68 AS funkyd
RUN mkdir /app
WORKDIR /app
COPY . .
RUN make clean
RUN make funkyd
EXPOSE 53/tcp
EXPOSE 53/udp
EXPOSE 54321/tcp
CMD ["sh"]

FROM funkyd AS deployable
ENTRYPOINT ["/app/funkyd", "--conf", "/etc/funkyd/funkyd.conf"]
