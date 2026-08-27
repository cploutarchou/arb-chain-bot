# Postgres 16 server + pgBackRest client for the restore drill Job.
FROM postgres:16
RUN apt-get update && apt-get install -y --no-install-recommends pgbackrest wget \
 && rm -rf /var/lib/apt/lists/*
USER postgres
