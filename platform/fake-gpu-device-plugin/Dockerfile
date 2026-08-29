FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY *.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/fake-gpu-device-plugin .

FROM alpine:3.20
COPY --from=build /out/fake-gpu-device-plugin /usr/local/bin/fake-gpu-device-plugin
ENTRYPOINT ["/usr/local/bin/fake-gpu-device-plugin"]
