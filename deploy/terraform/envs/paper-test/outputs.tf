# Consumed by the deploy pipeline to render values-<env>.generated.yaml.
output "database_cidr" { value = module.network.database_cidr }
output "redis_cidr" { value = module.network.redis_cidr }
output "recordings_bucket" { value = module.object_storage.recordings_bucket }
output "backups_bucket" { value = module.object_storage.backups_bucket }
output "postgres_endpoint" { value = module.postgres.endpoint }
output "secret_prefix" { value = module.secrets.secret_prefix }
output "cluster_name" { value = module.kubernetes.cluster_name }
