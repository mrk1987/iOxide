FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /ioxide ./cmd/ioxide

FROM alpine:3
RUN apk add --no-cache ca-certificates && adduser -D -h /data ioxide
COPY --from=build /ioxide /usr/local/bin/ioxide
USER ioxide
# Credentials are stored under $XDG_CONFIG_HOME/ioxide/auth.json.
ENV XDG_CONFIG_HOME=/data
VOLUME /data
EXPOSE 8081
ENTRYPOINT ["ioxide"]
CMD ["-listen", "0.0.0.0:8081", "serve"]
