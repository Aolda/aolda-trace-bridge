# syntax=docker/dockerfile:1.7

FROM golang:1.24-bookworm AS go-build

WORKDIR /src
COPY go.mod go.sum* ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -o /out/osprofiler-tempo-bridge ./cmd/osprofiler-tempo-bridge

FROM python:3.12-slim-bookworm

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1

WORKDIR /app
COPY requirements.txt /app/requirements.txt
RUN --mount=type=cache,target=/root/.cache/pip pip install -r /app/requirements.txt
RUN python -c "from osprofiler.drivers import base; base.get_driver('redis://:pw@127.0.0.1:6379/0', conf={})"

COPY --from=go-build /out/osprofiler-tempo-bridge /usr/local/bin/osprofiler-tempo-bridge
COPY helper /app/helper
COPY config.example.yaml /etc/osprofiler-tempo-bridge/config.yaml

ENTRYPOINT ["/usr/local/bin/osprofiler-tempo-bridge"]
