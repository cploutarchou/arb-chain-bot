# Terraform skeleton

Provider-neutral layout: `modules/` hold the resource contracts
(variables + outputs) and one documented example implementation per
module using the AWS provider (`example.tf`). Swap the example file for
GCP/Azure/Hetzner equivalents without changing `envs/`. There are no
credentials here: CI authenticates with OIDC (see
`.github/workflows/deploy.yml`) and state lives in a remote backend
configured per env in `backend.tf`.

```
envs/paper-test   envs/prod        # one root module per environment
modules/network                    # VPC, private subnets, NAT, egress allow-list
modules/kubernetes                 # managed k8s cluster + node pools
modules/postgres                   # managed Postgres HA (multi-AZ, PITR, read replica)
modules/redis                      # managed Redis (fan-out, rate gates) with AUTH + TLS
modules/object-storage             # recordings bucket + lifecycle + versioning + backups bucket
modules/secrets                    # KMS key + secret paths + ESO ClusterSecretStore role
```

Rules:
- `terraform fmt -check` and `terraform validate` run in CI for every env.
- `prevent_destroy` on the Postgres instance and both buckets.
- Every module output that a Helm value needs (DB subnet CIDR, bucket
  name, IAM role ARN) is exported from the env root and written to
  `values-<env>.generated.yaml` by the pipeline, not typed by hand.
- LIVE execution is not an infrastructure concern: there is no switch
  here to enable it.
