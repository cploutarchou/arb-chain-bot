variable "name" { type = string }
variable "environment" { type = string }
variable "oidc_issuer" {
  type        = string
  description = "Cluster OIDC issuer for workload identity (ESO + arbd service accounts)."
}
variable "secret_names" {
  description = "Secret paths the chart references (values.externalSecrets.keys). Values are created EMPTY and filled out-of-band by an operator; terraform never sees them."
  type        = list(string)
  default     = ["database-url", "secret-key", "admin-email", "admin-password", "telegram-token", "telegram-allowlist"]
}
variable "tags" {
  type    = map(string)
  default = {}
}
