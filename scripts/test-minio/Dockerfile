# syntax=docker/dockerfile:1.6
# Test-only replacement for unavailable MinIO community registry images.
# Official source-only distribution/build instructions:
# https://github.com/minio/minio#source-only-distribution
# https://github.com/minio/mc#install-from-source
# Both source commits and Docker Official Images are immutable; no fork image,
# build secret, repository file, or runtime credential enters this context.
FROM golang:1.25.10-alpine@sha256:8d22e29d960bc50cd025d93d5b7c7d220b1ee9aa7a239b3c8f55a57e987e8d45 AS build
ENV CGO_ENABLED=0 \
    GOTOOLCHAIN=local \
    GOPROXY=https://proxy.golang.org \
    GOSUMDB=sum.golang.org
RUN go install -trimpath github.com/minio/minio@7aac2a2c5b7c882e68c1ce017d8256be2feea27f \
    && go install -trimpath github.com/minio/mc@77f82e18b5401a65958f1619df6ebb994634bd88 \
    && mkdir -p /out/licenses \
    && cp "$(go list -m -f '{{.Dir}}' github.com/minio/minio@7aac2a2c5b7c882e68c1ce017d8256be2feea27f)/LICENSE" /out/licenses/minio-LICENSE \
    && cp "$(go list -m -f '{{.Dir}}' github.com/minio/mc@77f82e18b5401a65958f1619df6ebb994634bd88)/LICENSE" /out/licenses/mc-LICENSE

FROM alpine:3.22@sha256:5291449c3df73caf6ed85e649dec1b9e818b39a5d8c871e97afc13e9cd5e8fa8
COPY --from=build /go/bin/minio /go/bin/mc /usr/local/bin/
COPY --from=build /out/licenses /usr/share/licenses/minio
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
EXPOSE 9000
ENTRYPOINT ["minio"]
