output "vpc_id" { value = aws_vpc.this.id }
output "private_subnet_ids" { value = aws_subnet.private[*].id }
output "database_subnet_ids" { value = aws_subnet.database[*].id }
output "database_cidr" { value = cidrsubnet(var.cidr, 4, 8) }
output "redis_cidr" { value = cidrsubnet(var.cidr, 4, 9) }
