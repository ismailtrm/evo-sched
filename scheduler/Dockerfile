FROM golang:1.25 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/evok8s-scheduler ./main.go

FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/evok8s-scheduler /evok8s-scheduler
USER nonroot:nonroot
ENTRYPOINT ["/evok8s-scheduler"]
