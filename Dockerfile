FROM scratch
ARG TARGETPLATFORM
COPY $TARGETPLATFORM/helm-chart-bumper /helm-chart-bumper
ENTRYPOINT ["/helm-chart-bumper"]