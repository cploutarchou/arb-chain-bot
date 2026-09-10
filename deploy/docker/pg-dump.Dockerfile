# Postgres 16 client + aws-cli v2 for the logical-dump CronJob
# (deploy/postgres/pgdump-cronjob.yaml, backup option B). aws uses the
# pod's workload identity (IRSA/web-id) — no static keys in the image.
FROM postgres:16
RUN apt-get update && apt-get install -y --no-install-recommends wget curl unzip ca-certificates \
 && rm -rf /var/lib/apt/lists/* \
 && curl -sSfL https://awscli.amazonaws.com/awscli-exe-linux-x86_64.zip -o /tmp/aws.zip \
 && unzip -q /tmp/aws.zip -d /tmp \
 && /tmp/aws/install \
 && rm -rf /tmp/aws* \
 && aws --version
USER postgres
