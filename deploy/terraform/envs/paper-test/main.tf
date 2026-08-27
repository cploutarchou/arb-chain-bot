locals {
  name = "arb-paper-test"
  env  = "paper-test"
  tags = { project = "arb-platform", environment = local.env, mode = "PAPER" }
}

module "network" {
  source = "../../modules/network"
  name   = local.name
  cidr   = "10.20.0.0/16"
  azs    = var.azs
  tags   = local.tags
}

module "secrets" {
  source      = "../../modules/secrets"
  name        = local.name
  environment = local.env
  oidc_issuer = module.kubernetes.oidc_issuer
  tags        = local.tags
}

module "kubernetes" {
  source     = "../../modules/kubernetes"
  name       = local.name
  subnet_ids = module.network.private_subnet_ids
  tags       = local.tags
  node_pools = {
    engine = { instance_type = "m6i.large", min_size = 1, max_size = 2, desired_size = 1, spot = false, labels = { "arb.io/pool" = "engine" } }
    web    = { instance_type = "t3.medium", min_size = 1, max_size = 3, desired_size = 1, spot = true, labels = { "arb.io/pool" = "web" } }
  }
}

module "postgres" {
  source                = "../../modules/postgres"
  name                  = local.name
  instance_class        = "db.t4g.medium"
  allocated_storage_gb  = 100
  multi_az              = true
  read_replicas         = 0
  backup_retention_days = 7
  subnet_ids            = module.network.database_subnet_ids
  allowed_cidrs         = ["10.20.0.0/16"]
  kms_key_arn           = module.secrets.kms_key_arn
  tags                  = local.tags
}

module "redis" {
  source        = "../../modules/redis"
  name          = local.name
  subnet_ids    = module.network.database_subnet_ids
  allowed_cidrs = ["10.20.0.0/16"]
  kms_key_arn   = module.secrets.kms_key_arn
  tags          = local.tags
}

module "object_storage" {
  source                 = "../../modules/object-storage"
  name                   = local.name
  kms_key_arn            = module.secrets.kms_key_arn
  recordings_expire_days = 180
  backups_retention_days = 30
  tags                   = local.tags
}
