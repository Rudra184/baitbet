# Single static binary, no Chromium, no browser engine - the whole point
# of keeping this service's production image lean (PLAN.md §1.4, §3.3).
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /sim-server ./cmd/server

FROM gcr.io/distroless/static-debian12
COPY --from=build /sim-server /sim-server
EXPOSE 8766
ENTRYPOINT ["/sim-server"]
