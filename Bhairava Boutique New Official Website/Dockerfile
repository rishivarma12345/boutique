FROM golang:1.25-alpine AS build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' -o /out/boutique .

FROM alpine:3.22
WORKDIR /app
RUN adduser -D -H appuser
COPY --from=build /out/boutique /app/boutique
COPY templates ./templates
COPY static ./static
COPY database ./database
USER appuser
ENV PORT=8080
EXPOSE 8080
CMD ["/app/boutique"]
