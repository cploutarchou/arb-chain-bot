# Example: managed Postgres HA (AWS RDS Multi-AZ, PITR on, storage
# encrypted with the env KMS key, managed master password — nothing
# passes through terraform state in clear text).
resource "aws_db_subnet_group" "this" {
  name       = var.name
  subnet_ids = var.subnet_ids
  tags       = var.tags
}

resource "aws_security_group" "db" {
  name   = "${var.name}-db"
  vpc_id = data.aws_subnet.first.vpc_id

  ingress {
    from_port   = 5432
    to_port     = 5432
    protocol    = "tcp"
    cidr_blocks = var.allowed_cidrs
  }
}

data "aws_subnet" "first" {
  id = var.subnet_ids[0]
}

resource "aws_db_parameter_group" "this" {
  name   = var.name
  family = "postgres${var.engine_version}"

  # WAL settings for PITR + logical replication of the tick tables.
  parameter {
    name  = "rds.logical_replication"
    value = "1"
  }
  parameter {
    name  = "log_min_duration_statement"
    value = "500"
  }
}

resource "aws_db_instance" "primary" {
  identifier                  = var.name
  engine                      = "postgres"
  engine_version              = var.engine_version
  instance_class              = var.instance_class
  allocated_storage           = var.allocated_storage_gb
  max_allocated_storage       = var.max_allocated_storage_gb
  storage_type                = "gp3"
  storage_encrypted           = true
  kms_key_id                  = var.kms_key_arn
  db_name                     = var.database_name
  username                    = var.master_username
  manage_master_user_password = true
  multi_az                    = var.multi_az
  db_subnet_group_name        = aws_db_subnet_group.this.name
  vpc_security_group_ids      = [aws_security_group.db.id]
  parameter_group_name        = aws_db_parameter_group.this.name

  backup_retention_period   = var.backup_retention_days # enables PITR
  backup_window             = "02:00-03:00"
  maintenance_window        = "sun:03:30-sun:04:30"
  copy_tags_to_snapshot     = true
  deletion_protection       = true
  skip_final_snapshot       = false
  final_snapshot_identifier = "${var.name}-final"

  performance_insights_enabled          = true
  performance_insights_kms_key_id       = var.kms_key_arn
  performance_insights_retention_period = 7
  monitoring_interval                   = 60

  tags = var.tags

  lifecycle {
    prevent_destroy = true
  }
}

resource "aws_db_instance" "replica" {
  count               = var.read_replicas
  identifier          = "${var.name}-replica-${count.index}"
  replicate_source_db = aws_db_instance.primary.identifier
  instance_class      = var.instance_class
  storage_encrypted   = true
  kms_key_id          = var.kms_key_arn
  tags                = var.tags
}
