# Amazon EFS Module for Vidra Core Platform
# Creates EFS for ReadWriteMany persistent volumes (500GB+ shared storage)

locals {
  name = "${var.project_name}-${var.environment}-efs"

  common_tags = merge(
    var.tags,
    {
      Name   = local.name
      Module = "efs"
    }
  )
}

# KMS Key for EFS encryption
resource "aws_kms_key" "efs" {
  count = var.enable_encryption ? 1 : 0

  description             = "KMS key for EFS encryption"
  deletion_window_in_days = 30
  enable_key_rotation     = true

  tags = local.common_tags
}

resource "aws_kms_alias" "efs" {
  count = var.enable_encryption ? 1 : 0

  name          = "alias/${local.name}"
  target_key_id = aws_kms_key.efs[0].key_id
}

# EFS File System
resource "aws_efs_file_system" "main" {
  creation_token = local.name
  encrypted      = var.enable_encryption
  kms_key_id     = var.enable_encryption ? aws_kms_key.efs[0].arn : null

  performance_mode                = var.performance_mode
  throughput_mode                 = var.throughput_mode
  provisioned_throughput_in_mibps = var.throughput_mode == "provisioned" ? var.provisioned_throughput_in_mibps : null

  lifecycle_policy {
    transition_to_ia = var.transition_to_ia
  }

  dynamic "lifecycle_policy" {
    for_each = var.transition_to_archive != null ? [1] : []
    content {
      transition_to_archive = var.transition_to_archive
    }
  }

  tags = local.common_tags
}

# EFS Mount Targets (one per AZ)
resource "aws_efs_mount_target" "main" {
  count = length(var.subnet_ids)

  file_system_id  = aws_efs_file_system.main.id
  subnet_id       = var.subnet_ids[count.index]
  security_groups = [var.security_group_id]
}

# EFS Access Points for different workloads
resource "aws_efs_access_point" "storage" {
  file_system_id = aws_efs_file_system.main.id

  root_directory {
    path = "/storage"
    creation_info {
      owner_gid   = 1000
      owner_uid   = 1000
      permissions = "755"
    }
  }

  posix_user {
    gid = 1000
    uid = 1000
  }

  tags = merge(
    local.common_tags,
    {
      Name = "${local.name}-storage"
    }
  )
}

resource "aws_efs_access_point" "quarantine" {
  file_system_id = aws_efs_file_system.main.id

  root_directory {
    path = "/quarantine"
    creation_info {
      owner_gid   = 1000
      owner_uid   = 1000
      permissions = "755"
    }
  }

  posix_user {
    gid = 1000
    uid = 1000
  }

  tags = merge(
    local.common_tags,
    {
      Name = "${local.name}-quarantine"
    }
  )
}

# EFS Backup Policy
resource "aws_efs_backup_policy" "main" {
  count = var.enable_backup ? 1 : 0

  file_system_id = aws_efs_file_system.main.id

  backup_policy {
    status = "ENABLED"
  }
}

# CloudWatch Alarms
resource "aws_cloudwatch_metric_alarm" "burst_credit_balance" {
  count = var.create_cloudwatch_alarms && var.throughput_mode == "bursting" ? 1 : 0

  alarm_name          = "${local.name}-low-burst-credit"
  comparison_operator = "LessThanThreshold"
  evaluation_periods  = "2"
  metric_name         = "BurstCreditBalance"
  namespace           = "AWS/EFS"
  period              = "300"
  statistic           = "Average"
  threshold           = "1000000000000" # 1 TB
  alarm_description   = "EFS burst credit balance is low"
  alarm_actions       = var.alarm_actions

  dimensions = {
    FileSystemId = aws_efs_file_system.main.id
  }

  tags = local.common_tags
}

resource "aws_cloudwatch_metric_alarm" "percent_io_limit" {
  count = var.create_cloudwatch_alarms ? 1 : 0

  alarm_name          = "${local.name}-high-io"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = "2"
  metric_name         = "PercentIOLimit"
  namespace           = "AWS/EFS"
  period              = "300"
  statistic           = "Average"
  threshold           = "95"
  alarm_description   = "EFS is approaching I/O limit"
  alarm_actions       = var.alarm_actions

  dimensions = {
    FileSystemId = aws_efs_file_system.main.id
  }

  tags = local.common_tags
}
