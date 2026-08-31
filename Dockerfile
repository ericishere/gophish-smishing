# Minify client side assets (JavaScript)
FROM node:18-bullseye AS build-js

RUN npm install gulp gulp-cli -g

WORKDIR /build
COPY . .
RUN npm install
RUN gulp


# Build Golang binary
FROM golang:1.21 AS build-golang

WORKDIR /go/src/github.com/gophish/gophish
COPY . .
RUN go mod download && go build -v


# Runtime container
FROM debian:stable-slim

RUN useradd -m -d /opt/gophish -s /bin/bash app

RUN apt-get update && \
	apt-get install --no-install-recommends -y jq libcap2-bin ca-certificates && \
	apt-get clean && \
	rm -rf /var/lib/apt/lists/* /tmp/* /var/tmp/*

WORKDIR /opt/gophish
COPY --from=build-golang /go/src/github.com/gophish/gophish/ ./
COPY --from=build-js /build/static/js/dist/ ./static/js/dist/
COPY --from=build-js /build/static/css/dist/ ./static/css/dist/
COPY --from=build-golang /go/src/github.com/gophish/gophish/config.json ./
RUN chown app. config.json

RUN setcap 'cap_net_bind_service=+ep' /opt/gophish/gophish
RUN sed -i 's/\r$//' docker/run.sh && chmod 0755 docker/run.sh

# Create the data directory for the SQLite DB and hand it to the app user.
# Docker propagates this ownership to the named volume mounted at /data,
# so gophish (running as 'app') can create /data/gophish.db on first run
# and initialize a fresh database from the default migrations.
RUN mkdir -p /data && chown app:app /data

USER app
# Pre-create a writable config.json.tmp so run.sh (running as 'app') can
# rewrite config.json via jq at container start.
RUN touch config.json.tmp

EXPOSE 3333 8080 8443 80

CMD ["./docker/run.sh"]
