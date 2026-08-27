output "endpoint" { value = aws_db_instance.primary.address }
output "reader_endpoint" { value = try(aws_db_instance.replica[0].address, null) }
output "port" { value = aws_db_instance.primary.port }
output "master_secret_arn" {
  description = "Managed master password secret; the app URL is derived into the arb/<env>/database-url secret by the secrets module."
  value       = aws_db_instance.primary.master_user_secret[0].secret_arn
}
