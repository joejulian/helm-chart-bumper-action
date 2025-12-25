FROM gcr.io/distroless/static-debian12:nonroot
ARG TARGETPLATFORM
COPY $TARGETPLATFORM/helm-chart-bumper /helm-chart-bumper
# make this work for github actions/checkout
USER 1001
ENTRYPOINT ["/helm-chart-bumper"]
