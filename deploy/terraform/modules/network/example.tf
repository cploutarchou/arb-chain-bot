# Example implementation (AWS). Private subnets for nodes, isolated
# subnets for Postgres/Redis, NAT for exchange egress.
resource "aws_vpc" "this" {
  cidr_block           = var.cidr
  enable_dns_hostnames = true
  tags                 = merge(var.tags, { Name = var.name })
}

resource "aws_subnet" "private" {
  count             = length(var.azs)
  vpc_id            = aws_vpc.this.id
  availability_zone = var.azs[count.index]
  cidr_block        = cidrsubnet(var.cidr, 4, count.index)
  tags              = merge(var.tags, { Name = "${var.name}-private-${count.index}", tier = "nodes" })
}

resource "aws_subnet" "database" {
  count             = length(var.azs)
  vpc_id            = aws_vpc.this.id
  availability_zone = var.azs[count.index]
  cidr_block        = cidrsubnet(cidrsubnet(var.cidr, 4, 8), 4, count.index)
  tags              = merge(var.tags, { Name = "${var.name}-db-${count.index}", tier = "data" })
}
