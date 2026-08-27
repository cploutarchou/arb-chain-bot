variable "name" {
  type        = string
  description = "Environment prefix, e.g. arb-prod"
}

variable "cidr" {
  type        = string
  description = "VPC CIDR"
  default     = "10.30.0.0/16"
}

variable "azs" {
  type        = list(string)
  description = "Availability zones (>=2 for HA)"
}

variable "tags" {
  type    = map(string)
  default = {}
}
