FROM golang:1.25-alpine AS build

ARG LDFLAGS

WORKDIR /app

COPY . .

RUN go build -o k8s-pv-mount-point-exporter -ldflags "$LDFLAGS" cmd/main.go 

FROM alpine

WORKDIR /app

COPY --from=build /app/k8s-pv-mount-point-exporter k8s-pv-mount-point-exporter

CMD ["/app/k8s-pv-mount-point-exporter"]