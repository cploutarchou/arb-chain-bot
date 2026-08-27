locals {
  name = "arb-prod"
  env  = "prod"
  # mode=PAPER: production runs paper execution only until the
  # production gate; the gate is a code change, not a terraform variable.
  tags = { project = "arb-platform", environment = local.env, mode = "PAPER" }
}

module "network" {
  source = "../../modules/network"
  name   = local.name
  cidr   = "10.30.0.0/16"
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
}

module "postgres" {
  source                = "../../modules/postgres"
  name                  = local.name
  instance_class        = "db.m6g.large"
  allocated_storage_gb  = 500
  multi_az              = true
  read_replicas         = 1
  backup_retention_days = 14
  subnet_ids            = module.network.database_subnet_ids
  allowed_cidrs         = ["10.30.0.0/16"]
  kms_key_arn           = module.secrets.kms_key_arn
  tags                  = local.tags
}

module "redis" {
  source             = "../../modules/redis"
  name               = local.name
  node_type          = "cache.m6g.large"
  replicas_per_shard = 2
  subnet_ids         = module.network.database_subnet_ids
  allowed_cidrs      = ["10.30.0.0/16"]
  kms_key_arn        = module.secrets.kms_key_arn
  tags               = local.tags
}

module "object_storage" {
  source      = "../../modules/object-storage"
  name        = local.name
  kms_key_arn = module.secrets.kms_key_arn
  tags        = local.tags
}
