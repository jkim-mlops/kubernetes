FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/mlsim .

# Alpine rather than distroless on purpose: this is a debugging lab, and being
# able to `kubectl exec` into a pod with a shell and curl is half the exercise.
FROM alpine:3.20
RUN apk add --no-cache ca-certificates curl
COPY --from=build /out/mlsim /usr/local/bin/mlsim
ENTRYPOINT ["/usr/local/bin/mlsim"]
