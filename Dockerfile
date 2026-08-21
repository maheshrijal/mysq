FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/mysqldot ./cmd/mysqldot

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/mysqldot /usr/local/bin/mysqldot
ENTRYPOINT ["/usr/local/bin/mysqldot"]
