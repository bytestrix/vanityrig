FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /vanityrig ./cmd/vanityrig

FROM alpine:3
COPY --from=build /vanityrig /usr/local/bin/vanityrig
ENTRYPOINT ["vanityrig"]
