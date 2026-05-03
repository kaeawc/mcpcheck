FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/mcpcheck ./cmd/mcpcheck

FROM scratch
COPY --from=build /out/mcpcheck /mcpcheck
ENTRYPOINT ["/mcpcheck"]
