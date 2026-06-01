FROM gcr.io/distroless/static:nonroot
COPY straddler /straddler
ENTRYPOINT ["/straddler"]
