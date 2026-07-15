# Build context is the repository root: the rebar3 grpc codegen reads the shared
# protobuf contracts at ../../proto relative to this service directory.
FROM erlang:27-alpine AS builder

RUN apk add --no-cache git build-base

WORKDIR /build
# Proto contracts first (rarely change) so the layer caches well.
COPY proto /build/proto
COPY services/session-manager /build/services/session-manager

WORKDIR /build/services/session-manager
RUN rebar3 as prod release

# --- runtime ---
# The Alpine version MUST match the erlang:27-alpine builder's base (3.24), or
# the bundled crypto NIF fails to load against a different OpenSSL ABI
# (e.g. "EVP_MD_CTX_get_size_ex: symbol not found").
FROM alpine:3.24

RUN apk add --no-cache libstdc++ ncurses-libs openssl && \
    adduser -D -u 10001 acg

COPY --from=builder /build/services/session-manager/_build/prod/rel/session_manager /opt/session_manager

USER acg
EXPOSE 50053
ENV ACG_SESSION_PORT=50053

ENTRYPOINT ["/opt/session_manager/bin/session_manager"]
CMD ["foreground"]
