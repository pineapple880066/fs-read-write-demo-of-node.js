FROM golang:1.22

# TS RAG bridge needs node runtime inside app container.
RUN apt-get update \
    && apt-get install -y --no-install-recommends nodejs npm \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /workspace

