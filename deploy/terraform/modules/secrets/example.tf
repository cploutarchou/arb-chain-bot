# Example: one KMS key per environment; secret shells under
# arb/<env>/<name> that operators populate via the console/CLI with
# their own credentials (write-only from the platform's point of view);
# an IAM role for External Secrets Operator to read them.
resource "aws_kms_key" "env" {
  description             = "arb ${var.environment} data key"
  deletion_window_in_days = 30
  enable_key_rotation     = true
  tags                    = var.tags
}

resource "aws_kms_alias" "env" {
  name          = "alias/${var.name}"
  target_key_id = aws_kms_key.env.key_id
}

resource "aws_secretsmanager_secret" "shell" {
  for_each   = toset(var.secret_names)
  name       = "arb/${var.environment}/${each.key}"
  kms_key_id = aws_kms_key.env.arn
  tags       = var.tags
  # No secret_version here on purpose: values are set out of band.
}

data "aws_iam_policy_document" "eso_assume" {
  statement {
    actions = ["sts:AssumeRoleWithWebIdentity"]
    principals {
      type        = "Federated"
      identifiers = ["arn:aws:iam::ACCOUNT_ID:oidc-provider/${replace(var.oidc_issuer, "https://", "")}"]
    }
    condition {
      test     = "StringEquals"
      variable = "${replace(var.oidc_issuer, "https://", "")}:sub"
      values   = ["system:serviceaccount:external-secrets:external-secrets"]
    }
  }
}

resource "aws_iam_role" "eso" {
  name               = "${var.name}-external-secrets"
  assume_role_policy = data.aws_iam_policy_document.eso_assume.json
}

data "aws_iam_policy_document" "eso_read" {
  statement {
    actions   = ["secretsmanager:GetSecretValue", "secretsmanager:DescribeSecret"]
    resources = ["arn:aws:secretsmanager:*:*:secret:arb/${var.environment}/*"]
  }
  statement {
    actions   = ["kms:Decrypt"]
    resources = [aws_kms_key.env.arn]
  }
}

resource "aws_iam_role_policy" "eso_read" {
  role   = aws_iam_role.eso.id
  policy = data.aws_iam_policy_document.eso_read.json
}
