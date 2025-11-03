# Stage de build
FROM golang:1.25-alpine AS backend
RUN apk update && apk add --no-cache ca-certificates git make tzdata

WORKDIR /src

# permitir que buildx injete TARGETOS/TARGETARCH (default amd64)
ARG TARGETOS=linux
ARG TARGETARCH=amd64

# usar variáveis para compilação multiplataforma
ENV CGO_ENABLED=0 \
    GOOS=${TARGETOS} \
    GOARCH=${TARGETARCH}

# aproveitar cache de dependências
COPY go.mod go.sum ./
RUN go mod download

# copiar código e compilar
COPY . .
RUN go build -ldflags="-s -w" -o /bin/spot-price-exporter .

# Stage final mínimo
FROM alpine:3
RUN apk add --no-cache ca-certificates
COPY --from=backend /bin/spot-price-exporter /bin/spot-price-exporter
ENTRYPOINT ["/bin/spot-price-exporter"]
