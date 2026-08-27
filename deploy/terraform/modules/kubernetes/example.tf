# Example implementation (AWS EKS). Private endpoint only; the CI runner
# reaches it through the OIDC-assumed deploy role + a bastion-less
# private link, never via a public API endpoint.
resource "aws_eks_cluster" "this" {
  name     = var.name
  version  = var.kubernetes_version
  role_arn = aws_iam_role.cluster.arn

  vpc_config {
    subnet_ids              = var.subnet_ids
    endpoint_private_access = true
    endpoint_public_access  = false
  }

  enabled_cluster_log_types = ["api", "audit", "authenticator"]
  tags                      = var.tags
}

resource "aws_iam_role" "cluster" {
  name               = "${var.name}-cluster"
  assume_role_policy = data.aws_iam_policy_document.cluster_assume.json
}

data "aws_iam_policy_document" "cluster_assume" {
  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["eks.amazonaws.com"]
    }
  }
}

resource "aws_eks_node_group" "pool" {
  for_each        = var.node_pools
  cluster_name    = aws_eks_cluster.this.name
  node_group_name = each.key
  node_role_arn   = aws_iam_role.cluster.arn # placeholder: use a dedicated node role
  subnet_ids      = var.subnet_ids
  instance_types  = [each.value.instance_type]
  capacity_type   = each.value.spot ? "SPOT" : "ON_DEMAND"
  labels          = each.value.labels

  scaling_config {
    min_size     = each.value.min_size
    max_size     = each.value.max_size
    desired_size = each.value.desired_size
  }

  update_config {
    max_unavailable = 1
  }
}
