# Multi-architecture build for the gridflow engine.
# Build: docker buildx build --platform linux/amd64 --load -t go-task-check:amd64 .
#        docker buildx build --platform linux/arm64 --load -t go-task-check:arm64 .
# Run smoke-test: docker run --rm go-task-check:amd64 --smoke-test
FROM docker.m.daocloud.io/library/golang:1.26.3-bookworm AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ENV CGO_ENABLED=0 GOTOOLCHAIN=local
RUN go build -o /out/gridflow .

FROM docker.m.daocloud.io/library/alpine:3.20
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=builder /out/gridflow /app/gridflow
EXPOSE 8080
ENTRYPOINT ["/app/gridflow"]
CMD ["--addr=:8080", "--db=gridflow.db"]
