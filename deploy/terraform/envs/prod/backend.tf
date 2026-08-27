terraform {
  backend "s3" {
    # Placeholders; CI passes -backend-config from OIDC-scoped variables.
    bucket         = "REPLACE-tfstate-bucket"
    key            = "arb/prod/terraform.tfstate"
    region         = "eu-central-1"
    dynamodb_table = "REPLACE-tfstate-lock"
    encrypt        = true
  }
}
