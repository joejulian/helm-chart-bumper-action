FROM golang:1.23-alpine AS build
RUN apk add --no-cache ca-certificates
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/helm-chart-bumper ./cmd/helm-chart-bumper

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=build /out/helm-chart-bumper /usr/local/bin/helm-chart-bumper
ENTRYPOINT ["/usr/local/bin/helm-chart-bumper"]
