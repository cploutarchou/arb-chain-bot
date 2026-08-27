output "kms_key_arn" { value = aws_kms_key.env.arn }
output "eso_role_arn" { value = aws_iam_role.eso.arn }
output "secret_prefix" { value = "arb/${var.environment}/" }
