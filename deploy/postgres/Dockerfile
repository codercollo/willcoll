# Postgres with Willcoll's first-boot setup baked in (nothing bind-mounted).
# Build context: deploy/postgres/. Same image locally and in production.
FROM postgres:17

COPY init.sql check_rls.sql check_rls_one.sql /opt/willcoll/
COPY init.sh /docker-entrypoint-initdb.d/10-willcoll-init.sh
RUN chmod 0755 /docker-entrypoint-initdb.d/10-willcoll-init.sh
