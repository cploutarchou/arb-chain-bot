variable "name" { type = string }
variable "node_type" {
  type    = string
  default = "cache.t4g.small"
}
variable "replicas_per_shard" {
  type    = number
  default = 1
}
variable "subnet_ids" { type = list(string) }
variable "allowed_cidrs" { type = list(string) }
variable "kms_key_arn" { type = string }
variable "tags" {
  type    = map(string)
  default = {}
}
