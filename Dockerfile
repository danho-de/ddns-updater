FROM alpine:latest as alpine
RUN apk add -U --no-cache ca-certificates

FROM scratch
# The root SSL certificates are copied from apline into scratch, because otherwise the go application will fail 
# when connecting to any auth provader, because of the SSL package used
COPY --from=alpine /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
ADD ddns-updater app/
ADD config app/config/
WORKDIR /app
CMD ["./ddns-updater" ]