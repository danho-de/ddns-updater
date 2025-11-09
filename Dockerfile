FROM alpine:latest AS alpine
RUN apk add -U --no-cache ca-certificates

FROM scratch
# The root SSL certificates are copied from alpine into scratch
COPY --from=alpine /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
ADD ddns-updater app/
ADD config app/config/
WORKDIR /app
CMD ["./ddns-updater" ]