terraform {
  required_version = ">= 1.6.0"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.60"
    }
  }
}

provider "aws" {
  region = var.region
  # Credentials: OIDC-assumed role in CI, SSO profile locally. Nothing here.
  default_tags {
    tags = local.tags
  }
}
