# Base images are pinned by digest; Dependabot updates them weekly.
FROM golang:1.27-alpine@sha256:70bd7ddea8b6cbe5d6943cde279d6b67feb10cc924fb9100d1d8757c1575b280 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /ioxide ./cmd/ioxide

FROM alpine:3@sha256:f59abe656b692d901e184be3445e084786602108599d2a51d229c0f5a48c71bf
RUN apk add --no-cache ca-certificates && adduser -D -h /data ioxide
COPY --from=build /ioxide /usr/local/bin/ioxide
USER ioxide
# Credentials are stored under $XDG_CONFIG_HOME/ioxide/auth.json.
ENV XDG_CONFIG_HOME=/data
VOLUME /data
EXPOSE 8081
ENTRYPOINT ["ioxide"]
CMD ["-listen", "0.0.0.0:8081", "serve"]
