FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /pulso ./cmd/pulso

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /pulso /pulso
WORKDIR /data
EXPOSE 8080
ENTRYPOINT ["/pulso"]
CMD ["-c", "/data/pulso.json"]
