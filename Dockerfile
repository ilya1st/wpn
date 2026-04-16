FROM alpine:3.19

RUN apk add --no-cache iproute2 tcpdump iputils

WORKDIR /vpn

CMD ["/bin/sh"]
