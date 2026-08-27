variable "name" { type = string }
variable "kubernetes_version" {
  type    = string
  default = "1.30"
}
variable "subnet_ids" { type = list(string) }
variable "node_pools" {
  description = "Node pools: engine pool is pinned + on-demand; web pool may be spot."
  type = map(object({
    instance_type = string
    min_size      = number
    max_size      = number
    desired_size  = number
    spot          = bool
    labels        = map(string)
  }))
  default = {
    engine = { instance_type = "m6i.large", min_size = 2, max_size = 3, desired_size = 2, spot = false, labels = { "arb.io/pool" = "engine" } }
    web    = { instance_type = "t3.medium", min_size = 2, max_size = 6, desired_size = 2, spot = true, labels = { "arb.io/pool" = "web" } }
  }
}
variable "tags" {
  type    = map(string)
  default = {}
}
