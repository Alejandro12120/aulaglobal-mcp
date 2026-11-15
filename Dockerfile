FROM golang:1.26.9-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /server ./cmd/aulaglobal-mcp && mkdir /data && chown 65532:65532 /data
FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /server /aulaglobal-mcp
# MIT and the third-party licences require their notices to travel with the binary.
COPY LICENSE THIRD_PARTY_NOTICES.md /licenses/
COPY --from=build --chown=65532:65532 /data /data
USER 65532:65532
ENV AULAGLOBAL_DOWNLOAD_DIR=/data MCP_TRANSPORT=http MCP_ADDR=0.0.0.0:8080 AULAGLOBAL_IN_CONTAINER=1
VOLUME ["/data"]
EXPOSE 8080
ENTRYPOINT ["/aulaglobal-mcp"]
