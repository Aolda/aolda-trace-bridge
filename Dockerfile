FROM golang:1.24-bookworm AS go-build

WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/osprofiler-tempo-bridge ./cmd/osprofiler-tempo-bridge

FROM python:3.12-slim-bookworm

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1

WORKDIR /app
COPY requirements.txt /app/requirements.txt
RUN pip install --no-cache-dir -r /app/requirements.txt

COPY --from=go-build /out/osprofiler-tempo-bridge /usr/local/bin/osprofiler-tempo-bridge
COPY helper /app/helper
COPY config.example.yaml /etc/osprofiler-tempo-bridge/config.yaml

ENTRYPOINT ["/usr/local/bin/osprofiler-tempo-bridge"]

