FROM gcr.io/distroless/static-debian12:nonroot
ARG TARGETPLATFORM
COPY $TARGETPLATFORM/helm-chart-bumper /helm-chart-bumper
ENTRYPOINT ["/helm-chart-bumper"]
