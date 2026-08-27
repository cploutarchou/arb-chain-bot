# Example: two buckets — recordings (engine writes closed sessions,
# campaign tool reads) and backups (pgBackRest repo). Versioned,
# KMS-encrypted, public access blocked, lifecycle tiers.
locals {
  buckets = {
    recordings = "${var.name}-recordings"
    backups    = "${var.name}-pg-backups"
  }
}

resource "aws_s3_bucket" "recordings" {
  bucket = local.buckets.recordings
  tags   = var.tags
  lifecycle { prevent_destroy = true }
}

resource "aws_s3_bucket" "backups" {
  bucket = local.buckets.backups
  tags   = var.tags
  lifecycle { prevent_destroy = true }
}

resource "aws_s3_bucket_public_access_block" "all" {
  for_each                = { recordings = aws_s3_bucket.recordings.id, backups = aws_s3_bucket.backups.id }
  bucket                  = each.value
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_versioning" "all" {
  for_each = { recordings = aws_s3_bucket.recordings.id, backups = aws_s3_bucket.backups.id }
  bucket   = each.value
  versioning_configuration { status = "Enabled" }
}

resource "aws_s3_bucket_server_side_encryption_configuration" "all" {
  for_each = { recordings = aws_s3_bucket.recordings.id, backups = aws_s3_bucket.backups.id }
  bucket   = each.value
  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm     = "aws:kms"
      kms_master_key_id = var.kms_key_arn
    }
    bucket_key_enabled = true
  }
}

resource "aws_s3_bucket_lifecycle_configuration" "recordings" {
  bucket = aws_s3_bucket.recordings.id

  # Sessions referenced by a published campaign are tagged keep=true by
  # the campaign tool and never transition past cold or expire.
  rule {
    id     = "tiering"
    status = "Enabled"
    filter {
      and {
        prefix = "sessions/"
        tags   = { keep = "false" }
      }
    }
    transition {
      days          = var.recordings_hot_days
      storage_class = "STANDARD_IA"
    }
    transition {
      days          = var.recordings_cold_days
      storage_class = "GLACIER_IR"
    }
    dynamic "expiration" {
      for_each = var.recordings_expire_days > 0 ? [1] : []
      content { days = var.recordings_expire_days }
    }
    noncurrent_version_expiration { noncurrent_days = 30 }
    abort_incomplete_multipart_upload { days_after_initiation = 7 }
  }

  rule {
    id     = "evidence-cold-only"
    status = "Enabled"
    filter {
      and {
        prefix = "sessions/"
        tags   = { keep = "true" }
      }
    }
    transition {
      days          = var.recordings_cold_days
      storage_class = "GLACIER_IR"
    }
  }
}

resource "aws_s3_bucket_lifecycle_configuration" "backups" {
  bucket = aws_s3_bucket.backups.id
  rule {
    id     = "retention"
    status = "Enabled"
    filter { prefix = "" }
    transition {
      days          = 7
      storage_class = "STANDARD_IA"
    }
    expiration { days = var.backups_retention_days }
    noncurrent_version_expiration { noncurrent_days = 7 }
  }
}

# Object lock on backups bucket protects against ransomware-style deletes.
resource "aws_s3_bucket_object_lock_configuration" "backups" {
  bucket = aws_s3_bucket.backups.id
  rule {
    default_retention {
      mode = "COMPLIANCE"
      days = 30
    }
  }
}
