FROM golang:1.25 AS build
WORKDIR /app
# Copy only the backend directory (build context is backend/)
COPY . .
RUN CGO_ENABLED=0 go build -o /logos .

FROM gcr.io/distroless/base-debian12
COPY --from=build /logos /logos
COPY --from=build /app/agents/philosophers /agents/philosophers
ENV DATA_DIR=/
ENV PORT=8080
EXPOSE 8080
CMD ["/logos"]
