variable "name" { type = string }
variable "kms_key_arn" { type = string }
variable "recordings_hot_days" {
  type        = number
  description = "Days before closed recording sessions move to infrequent-access."
  default     = 30
}
variable "recordings_cold_days" {
  type        = number
  description = "Days before recordings move to archive tier. Campaign evidence referenced in docs/campaigns/ is tagged keep=true and excluded."
  default     = 180
}
variable "recordings_expire_days" {
  type        = number
  description = "Days before untagged recordings are deleted. 0 = never."
  default     = 730
}
variable "backups_retention_days" {
  type        = number
  description = "pgBackRest full+WAL retention in the backups bucket."
  default     = 90
}
variable "tags" {
  type    = map(string)
  default = {}
}
