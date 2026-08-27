variable "name" { type = string }
variable "engine_version" {
  type    = string
  default = "16"
}
variable "instance_class" {
  type    = string
  default = "db.m6g.large"
}
variable "allocated_storage_gb" {
  type    = number
  default = 200
}
variable "max_allocated_storage_gb" {
  type    = number
  default = 2000
}
variable "multi_az" {
  type        = bool
  description = "Synchronous standby in a second AZ (HA)."
  default     = true
}
variable "read_replicas" {
  type    = number
  default = 1
}
variable "backup_retention_days" {
  type        = number
  description = "PITR window kept by the managed service. Long-term copies come from deploy/postgres/ (pgBackRest to the backups bucket)."
  default     = 14
}
variable "subnet_ids" { type = list(string) }
variable "allowed_cidrs" {
  type        = list(string)
  description = "Node subnets allowed on 5432"
}
variable "kms_key_arn" { type = string }
variable "database_name" {
  type    = string
  default = "arb"
}
variable "master_username" {
  type    = string
  default = "arb"
}
variable "tags" {
  type    = map(string)
  default = {}
}
