set -e
BUILDNUM="$(git log --format=oneline | wc -l)"
BUILDNUM_CLEAN="$(echo -e "${BUILDNUM}" | tr -d '[:space:]')"

go fmt
env CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -ldflags="-X main.buildNum=$BUILDNUM_CLEAN" -o ddns-updater

